package arm64

import (
	"github.com/tetratelabs/wazero/internal/engine/wazevo/backend"
	"github.com/tetratelabs/wazero/internal/engine/wazevo/backend/regalloc"
	"github.com/tetratelabs/wazero/internal/engine/wazevo/ssa"
	"github.com/tetratelabs/wazero/internal/engine/wazevo/wazevoapi"
)

// EmitGoEntryPreamble implements backend.FunctionABI. This assumes `entrypoint` function (in abi_go_entry_arm64.s) passes:
//
//  1. First (execution context ptr) and Second arguments are already passed in x0, and x1.
//  2. param/result slice ptr in x19; the pointer to []uint64{} which is used to pass arguments and accept return values.
//  3. Go-allocated stack slice ptr in x26.
//
// also SP and FP are correct Go-runtime-based values, and LR is the return address to the Go-side caller.
func (a *abiImpl) EmitGoEntryPreamble() {
	root := a.constructGoEntryPreamble()
	a.m.encode(root)
}

var (
	executionContextPtrReg = x0VReg
	// callee-saved regs so that they can be used in the prologue and epilogue.
	paramResultSlicePtr      = x19VReg
	savedExecutionContextPtr = x20VReg
	// goAllocatedStackPtr is not used in the epilogue.
	goAllocatedStackPtr = x26VReg
	// paramResultSliceCopied is not used in the epilogue.
	paramResultSliceCopied = x25VReg
)

func (m *machine) goEntryPreamblePassArg(cur *instruction, paramSlicePtr regalloc.VReg, arg *backend.ABIArg) *instruction {
	typ := arg.Type
	bits := typ.Bits()
	isStackArg := arg.Kind == backend.ABIArgKindStack

	var loadTargetReg operand
	if !isStackArg {
		loadTargetReg = operandNR(arg.Reg)
	} else {
		switch typ {
		case ssa.TypeI32, ssa.TypeI64:
			loadTargetReg = operandNR(tmpRegVReg)
		case ssa.TypeF32, ssa.TypeF64, ssa.TypeV128:
			loadTargetReg = operandNR(v15VReg)
		default:
			panic("TODO?")
		}
	}

	var postIndexImm int64
	if typ == ssa.TypeV128 {
		postIndexImm = 16 // v128 is represented as 2x64-bit in Go slice.
	} else {
		postIndexImm = 8
	}
	loadMode := addressMode{kind: addressModeKindPostIndex, rn: paramSlicePtr, imm: postIndexImm}

	instr := m.allocateInstr()
	switch typ {
	case ssa.TypeI32:
		instr.asULoad(loadTargetReg, loadMode, 32)
	case ssa.TypeI64:
		instr.asULoad(loadTargetReg, loadMode, 64)
	case ssa.TypeF32:
		instr.asFpuLoad(loadTargetReg, loadMode, 32)
	case ssa.TypeF64:
		instr.asFpuLoad(loadTargetReg, loadMode, 64)
	case ssa.TypeV128:
		instr.asFpuLoad(loadTargetReg, loadMode, 128)
	}
	cur = linkInstr(cur, instr)

	if isStackArg {
		var storeMode addressMode
		cur, storeMode = m.resolveAddressModeForOffsetAndInsert(cur, arg.Offset, bits, spVReg)
		toStack := m.allocateInstr()
		toStack.asStore(loadTargetReg, storeMode, bits)
		cur = linkInstr(cur, toStack)
	}
	return cur
}

func (m *machine) goEntryPreamblePassResult(cur *instruction, resultSlicePtr regalloc.VReg, result *backend.ABIArg) *instruction {
	isStackArg := result.Kind == backend.ABIArgKindStack
	typ := result.Type
	bits := typ.Bits()

	var storeTargetReg operand
	if !isStackArg {
		storeTargetReg = operandNR(result.Reg)
	} else {
		switch typ {
		case ssa.TypeI32, ssa.TypeI64:
			storeTargetReg = operandNR(tmpRegVReg)
		case ssa.TypeF32, ssa.TypeF64, ssa.TypeV128:
			storeTargetReg = operandNR(v15VReg)
		default:
			panic("TODO?")
		}
	}

	var postIndexImm int64
	if typ == ssa.TypeV128 {
		postIndexImm = 16 // v128 is represented as 2x64-bit in Go slice.
	} else {
		postIndexImm = 8
	}

	if isStackArg {
		var loadMode addressMode
		cur, loadMode = m.resolveAddressModeForOffsetAndInsert(cur, result.Offset, bits, spVReg)
		toReg := m.allocateInstr()
		switch typ {
		case ssa.TypeI32, ssa.TypeI64:
			toReg.asULoad(storeTargetReg, loadMode, bits)
		case ssa.TypeF32, ssa.TypeF64, ssa.TypeV128:
			toReg.asFpuLoad(storeTargetReg, loadMode, bits)
		}
		cur = linkInstr(cur, toReg)
	}

	mode := addressMode{kind: addressModeKindPostIndex, rn: resultSlicePtr, imm: postIndexImm}
	instr := m.allocateInstr()
	instr.asStore(storeTargetReg, mode, bits)
	cur = linkInstr(cur, instr)
	return cur
}

func (a *abiImpl) constructGoEntryPreamble() (root *instruction) {
	m := a.m
	root = m.allocateNop()

	//// ----------------------------------- prologue ----------------------------------- ////

	// First, we save executionContextPtrReg into a callee-saved register so that it can be used in epilogue as well.
	// 		mov savedExecutionContextPtr, x0
	cur := a.move64(savedExecutionContextPtr, executionContextPtrReg, root)

	// Next, save the current FP, SP and LR into the wazevo.executionContext:
	// 		str fp, [savedExecutionContextPtr, #OriginalFramePointer]
	//      mov tmp, sp ;; sp cannot be str'ed directly.
	// 		str sp, [savedExecutionContextPtr, #OriginalStackPointer]
	// 		str lr, [savedExecutionContextPtr, #GoReturnAddress]
	cur = a.loadOrStoreAtExecutionContext(fpVReg, wazevoapi.ExecutionContextOffsets.OriginalFramePointer, true, cur)
	cur = a.move64(tmpRegVReg, spVReg, cur)
	cur = a.loadOrStoreAtExecutionContext(tmpRegVReg, wazevoapi.ExecutionContextOffsets.OriginalStackPointer, true, cur)
	cur = a.loadOrStoreAtExecutionContext(lrVReg, wazevoapi.ExecutionContextOffsets.GoReturnAddress, true, cur)

	// Next, adjust the Go-allocated stack pointer to reserve the arg/result spaces.
	// 		sub x28, x28, #stackSlotSize
	if stackSlotSize := a.alignedArgResultStackSlotSize(); stackSlotSize > 0 {
		if imm12Operand, ok := asImm12Operand(uint64(stackSlotSize)); ok {
			instr := m.allocateInstr()
			rd := operandNR(goAllocatedStackPtr)
			instr.asALU(aluOpSub, rd, rd, imm12Operand, true)
			cur = linkInstr(cur, instr)
		} else {
			panic("TODO: too large stack slot size")
		}
	}

	// Then, move the Go-allocated stack pointer to SP:
	// 		mov sp, x28
	cur = a.move64(spVReg, goAllocatedStackPtr, cur)

	prReg := paramResultSlicePtr
	if len(a.args) > 2 && len(a.rets) > 0 {
		// paramResultSlicePtr is modified during the execution of goEntryPreamblePassArg,
		// so copy it to another reg.
		cur = a.move64(paramResultSliceCopied, paramResultSlicePtr, cur)
		prReg = paramResultSliceCopied
	}
	for i := range a.args {
		if i < 2 {
			// module context ptr and execution context ptr are passed in x0 and x1 by the Go assembly function.
			continue
		}
		arg := &a.args[i]
		cur = m.goEntryPreamblePassArg(cur, prReg, arg)
	}

	// Call the real function coming after epilogue:
	// 		bl #<offset of real function from this instruction>
	// But at this point, we don't know the size of epilogue, so we emit a placeholder.
	bl := m.allocateInstr()
	cur = linkInstr(cur, bl)

	///// ----------------------------------- epilogue ----------------------------------- /////

	// Store the register results into paramResultSlicePtr.
	for i := range a.rets {
		cur = m.goEntryPreamblePassResult(cur, paramResultSlicePtr, &a.rets[i])
	}

	// Finally, restore the FP, SP and LR, and return to the Go code.
	// 		ldr fp, [savedExecutionContextPtr, #OriginalFramePointer]
	// 		ldr tmp, [savedExecutionContextPtr, #OriginalStackPointer]
	//      mov sp, tmp ;; sp cannot be str'ed directly.
	// 		ldr lr, [savedExecutionContextPtr, #GoReturnAddress]
	// 		ret ;; --> return to the Go code
	cur = a.loadOrStoreAtExecutionContext(fpVReg, wazevoapi.ExecutionContextOffsets.OriginalFramePointer, false, cur)
	cur = a.loadOrStoreAtExecutionContext(tmpRegVReg, wazevoapi.ExecutionContextOffsets.OriginalStackPointer, false, cur)
	cur = a.move64(spVReg, tmpRegVReg, cur)
	cur = a.loadOrStoreAtExecutionContext(lrVReg, wazevoapi.ExecutionContextOffsets.GoReturnAddress, false, cur)
	retInst := a.m.allocateInstr()
	retInst.asRet(nil)
	linkInstr(cur, retInst)

	///// ----------------------------------- epilogue end / real function begins ----------------------------------- /////

	// Now that we allocated all instructions needed, we can calculate the size of epilogue, and finalize the
	// bl instruction to call the real function coming after epilogue.
	var blAt, realFuncAt int64
	for _cur := root; _cur != nil; _cur = _cur.next {
		if _cur == bl {
			blAt = realFuncAt
		}
		realFuncAt += _cur.size()
	}
	bl.asCallImm(realFuncAt - blAt)
	return
}

func (a *abiImpl) move64(dst, src regalloc.VReg, prev *instruction) *instruction {
	instr := a.m.allocateInstr()
	instr.asMove64(dst, src)
	return linkInstr(prev, instr)
}

func (a *abiImpl) loadOrStoreAtExecutionContext(d regalloc.VReg, offset wazevoapi.Offset, store bool, prev *instruction) *instruction {
	instr := a.m.allocateInstr()
	mode := addressMode{kind: addressModeKindRegUnsignedImm12, rn: savedExecutionContextPtr, imm: offset.I64()}
	if store {
		instr.asStore(operandNR(d), mode, 64)
	} else {
		instr.asULoad(operandNR(d), mode, 64)
	}
	return linkInstr(prev, instr)
}

func linkInstr(prev, next *instruction) *instruction {
	prev.next = next
	next.prev = prev
	return next
}
