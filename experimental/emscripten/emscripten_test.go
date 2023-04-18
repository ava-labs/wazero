package emscripten

import (
	"bytes"
	"context"
	_ "embed"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/logging"
	internal "github.com/tetratelabs/wazero/internal/emscripten"
	"github.com/tetratelabs/wazero/internal/testing/binaryencoding"
	"github.com/tetratelabs/wazero/internal/testing/require"
	"github.com/tetratelabs/wazero/internal/wasm"
)

const (
	i32 = wasm.ValueTypeI32
	i64 = wasm.ValueTypeI64
	f32 = wasm.ValueTypeF32
	f64 = wasm.ValueTypeF64
)

// testCtx is an arbitrary, non-default context. Non-nil also prevents linter errors.
var testCtx = context.WithValue(context.Background(), struct{}{}, "arbitrary")

func TestNewFunctionExporterForModule(t *testing.T) {
	tests := []struct {
		name     string
		input    *wasm.Module
		expected emscriptenFns
	}{
		{
			name:     "empty",
			input:    &wasm.Module{},
			expected: emscriptenFns{},
		},
		{
			name: internal.FunctionNotifyMemoryGrowth,
			input: &wasm.Module{
				TypeSection: []wasm.FunctionType{
					{Params: []wasm.ValueType{i32}},
				},
				ImportSection: []wasm.Import{
					{
						Module: "env", Name: internal.FunctionNotifyMemoryGrowth,
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
				},
			},
			expected: []*wasm.HostFunc{internal.NotifyMemoryGrowth},
		},
		{
			name: "all result types",
			input: &wasm.Module{
				TypeSection: []wasm.FunctionType{
					{Params: []wasm.ValueType{i32}},
					{Params: []wasm.ValueType{i32}, Results: []wasm.ValueType{i32}},
					{Params: []wasm.ValueType{i32}, Results: []wasm.ValueType{i64}},
					{Params: []wasm.ValueType{i32}, Results: []wasm.ValueType{f32}},
					{Params: []wasm.ValueType{i32}, Results: []wasm.ValueType{f64}},
				},
				ImportSection: []wasm.Import{
					{
						Module: "env", Name: "invoke_v",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
					{
						Module: "env", Name: "invoke_i",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 1,
					},
					{
						Module: "env", Name: "invoke_p",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 1,
					},
					{
						Module: "env", Name: "invoke_j",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 2,
					},
					{
						Module: "env", Name: "invoke_f",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 3,
					},
					{
						Module: "env", Name: "invoke_d",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 4,
					},
				},
			},
			expected: []*wasm.HostFunc{
				{
					ExportName: "invoke_v",
					ParamTypes: []api.ValueType{i32},
					ParamNames: []string{"index"},
					Code:       wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{}}},
				},
				{
					ExportName:  "invoke_i",
					ParamTypes:  []api.ValueType{i32},
					ParamNames:  []string{"index"},
					ResultTypes: []api.ValueType{i32},
					Code:        wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{Results: []api.ValueType{i32}}}},
				},
				{
					ExportName:  "invoke_p",
					ParamTypes:  []api.ValueType{i32},
					ParamNames:  []string{"index"},
					ResultTypes: []api.ValueType{i32},
					Code:        wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{Results: []api.ValueType{i32}}}},
				},
				{
					ExportName:  "invoke_j",
					ParamTypes:  []api.ValueType{i32},
					ParamNames:  []string{"index"},
					ResultTypes: []api.ValueType{i64},
					Code:        wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{Results: []api.ValueType{i64}}}},
				},
				{
					ExportName:  "invoke_f",
					ParamTypes:  []api.ValueType{i32},
					ParamNames:  []string{"index"},
					ResultTypes: []api.ValueType{f32},
					Code:        wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{Results: []api.ValueType{f32}}}},
				},
				{
					ExportName:  "invoke_d",
					ParamTypes:  []api.ValueType{i32},
					ParamNames:  []string{"index"},
					ResultTypes: []api.ValueType{f64},
					Code:        wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{Results: []api.ValueType{f64}}}},
				},
			},
		},
		{
			name: "ignores other imports",
			input: &wasm.Module{
				TypeSection: []wasm.FunctionType{
					{Params: []wasm.ValueType{i32}},
				},
				ImportSection: []wasm.Import{
					{
						Module: "anv", Name: "invoke_v",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
					{
						Module: "env", Name: "invoke_v",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
					{
						Module: "env", Name: "grow",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
				},
			},
			expected: []*wasm.HostFunc{
				{
					ExportName: "invoke_v",
					ParamTypes: []api.ValueType{i32},
					ParamNames: []string{"index"},
					Code:       wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{}}},
				},
			},
		},
		{
			name: "invoke_v and " + internal.FunctionNotifyMemoryGrowth,
			input: &wasm.Module{
				TypeSection: []wasm.FunctionType{{Params: []wasm.ValueType{i32}}},
				ImportSection: []wasm.Import{
					{
						Module: "env", Name: "invoke_v",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
					{
						Module: "env", Name: internal.FunctionNotifyMemoryGrowth,
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
				},
			},
			expected: []*wasm.HostFunc{
				{
					ExportName: "invoke_v",
					ParamTypes: []api.ValueType{i32},
					ParamNames: []string{"index"},
					Code:       wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{}}},
				},
				internal.NotifyMemoryGrowth,
			},
		},
		{
			name: "invoke_vi",
			input: &wasm.Module{
				TypeSection: []wasm.FunctionType{
					{Params: []wasm.ValueType{i32, i32}},
				},
				ImportSection: []wasm.Import{
					{
						Module: "env", Name: "invoke_vi",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
				},
			},
			expected: []*wasm.HostFunc{
				{
					ExportName: "invoke_vi",
					ParamTypes: []api.ValueType{i32, i32},
					ParamNames: []string{"index", "a1"},
					Code:       wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{Params: []api.ValueType{i32}}}},
				},
			},
		},
		{
			name: "invoke_iiiii",
			input: &wasm.Module{
				TypeSection: []wasm.FunctionType{
					{
						Params:  []wasm.ValueType{i32, i32, i32, i32, i32},
						Results: []wasm.ValueType{i32},
					},
				},
				ImportSection: []wasm.Import{
					{
						Module: "env", Name: "invoke_iiiii",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
				},
			},
			expected: []*wasm.HostFunc{
				{
					ExportName:  "invoke_iiiii",
					ParamTypes:  []api.ValueType{i32, i32, i32, i32, i32},
					ParamNames:  []string{"index", "a1", "a2", "a3", "a4"},
					ResultTypes: []wasm.ValueType{i32},
					Code: wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{
						Params:  []api.ValueType{i32, i32, i32, i32},
						Results: []api.ValueType{i32},
					}}},
				},
			},
		},
		{
			name: "invoke_viiiddiiiiii",
			input: &wasm.Module{
				TypeSection: []wasm.FunctionType{
					{
						Params: []wasm.ValueType{i32, i32, i32, i32, f64, f64, i32, i32, i32, i32, i32, i32},
					},
				},
				ImportSection: []wasm.Import{
					{
						Module: "env", Name: "invoke_viiiddiiiiii",
						Type:     wasm.ExternTypeFunc,
						DescFunc: 0,
					},
				},
			},
			expected: []*wasm.HostFunc{
				{
					ExportName: "invoke_viiiddiiiiii",
					ParamTypes: []api.ValueType{i32, i32, i32, i32, f64, f64, i32, i32, i32, i32, i32, i32},
					ParamNames: []string{"index", "a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10", "a11"},
					Code: wasm.Code{GoFunc: &internal.InvokeFunc{FunctionType: &wasm.FunctionType{
						Params: []api.ValueType{i32, i32, i32, f64, f64, i32, i32, i32, i32, i32, i32},
					}}},
				},
			},
		},
	}

	for _, tt := range tests {
		tc := tt

		t.Run(tc.name, func(t *testing.T) {
			r := wazero.NewRuntime(testCtx)
			defer r.Close(testCtx)

			guest, err := r.CompileModule(testCtx, binaryencoding.EncodeModule(tc.input))
			require.NoError(t, err)

			exporter, err := NewFunctionExporterForModule(guest)
			require.NoError(t, err)
			actual := exporter.(emscriptenFns)

			require.Equal(t, len(tc.expected), len(actual))
			for i, expected := range tc.expected {
				require.Equal(t, expected, actual[i], actual[i].ExportName)
			}
		})
	}
}

// invokeWasm was generated by the following:
//
//	cd testdata; wat2wasm --debug-names invoke.wat
//
//go:embed testdata/invoke.wasm
var invokeWasm []byte

func TestInstantiateForModule(t *testing.T) {
	var log bytes.Buffer

	// Set context to one that has an experimental listener
	ctx := context.WithValue(testCtx, experimental.FunctionListenerFactoryKey{}, logging.NewLoggingListenerFactory(&log))

	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	compiled, err := r.CompileModule(ctx, invokeWasm)
	require.NoError(t, err)

	_, err = InstantiateForModule(ctx, r, compiled)
	require.NoError(t, err)

	mod, err := r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	require.NoError(t, err)

	tests := []struct {
		name, funcName          string
		tableOffset             int
		params, expectedResults []uint64
		expectedLog             string
	}{
		{
			name:            "invoke_i",
			funcName:        "call_v_i32",
			expectedResults: []uint64{42},
			expectedLog: `--> .call_v_i32(0)
	==> env.invoke_i(index=0)
		--> .v_i32()
		<-- 42
	<== 42
<-- 42
`,
		},
		{
			name:            "invoke_ii",
			funcName:        "call_i32_i32",
			tableOffset:     2,
			params:          []uint64{42},
			expectedResults: []uint64{42},
			expectedLog: `--> .call_i32_i32(2,42)
	==> env.invoke_ii(index=2,a1=42)
		--> .i32_i32(42)
		<-- 42
	<== 42
<-- 42
`,
		},
		{
			name:            "invoke_iii",
			funcName:        "call_i32i32_i32",
			tableOffset:     4,
			params:          []uint64{1, 2},
			expectedResults: []uint64{3},
			expectedLog: `--> .call_i32i32_i32(4,1,2)
	==> env.invoke_iii(index=4,a1=1,a2=2)
		--> .i32i32_i32(1,2)
		<-- 3
	<== 3
<-- 3
`,
		},
		{
			name:            "invoke_iiii",
			funcName:        "call_i32i32i32_i32",
			tableOffset:     6,
			params:          []uint64{1, 2, 4},
			expectedResults: []uint64{7},
			expectedLog: `--> .call_i32i32i32_i32(6,1,2,4)
	==> env.invoke_iiii(index=6,a1=1,a2=2,a3=4)
		--> .i32i32i32_i32(1,2,4)
		<-- 7
	<== 7
<-- 7
`,
		},
		{
			name:            "invoke_iiiii",
			funcName:        "calli32_i32i32i32i32_i32",
			tableOffset:     8,
			params:          []uint64{1, 2, 4, 8},
			expectedResults: []uint64{15},
			expectedLog: `--> .calli32_i32i32i32i32_i32(8,1,2,4,8)
	==> env.invoke_iiiii(index=8,a1=1,a2=2,a3=4,a4=8)
		--> .i32i32i32i32_i32(1,2,4,8)
		<-- 15
	<== 15
<-- 15
`,
		},
		{
			name:        "invoke_v",
			funcName:    "call_v_v",
			tableOffset: 10,
			expectedLog: `--> .call_v_v(10)
	==> env.invoke_v(index=10)
		--> .v_v()
		<--
	<==
<--
`,
		},
		{
			name:        "invoke_vi",
			funcName:    "call_i32_v",
			tableOffset: 12,
			params:      []uint64{42},
			expectedLog: `--> .call_i32_v(12,42)
	==> env.invoke_vi(index=12,a1=42)
		--> .i32_v(42)
		<--
	<==
<--
`,
		},
		{
			name:        "invoke_vii",
			funcName:    "call_i32i32_v",
			tableOffset: 14,
			params:      []uint64{1, 2},
			expectedLog: `--> .call_i32i32_v(14,1,2)
	==> env.invoke_vii(index=14,a1=1,a2=2)
		--> .i32i32_v(1,2)
		<--
	<==
<--
`,
		},
		{
			name:        "invoke_viii",
			funcName:    "call_i32i32i32_v",
			tableOffset: 16,
			params:      []uint64{1, 2, 4},
			expectedLog: `--> .call_i32i32i32_v(16,1,2,4)
	==> env.invoke_viii(index=16,a1=1,a2=2,a3=4)
		--> .i32i32i32_v(1,2,4)
		<--
	<==
<--
`,
		},
		{
			name:        "invoke_viiii",
			funcName:    "calli32_i32i32i32i32_v",
			tableOffset: 18,
			params:      []uint64{1, 2, 4, 8},
			expectedLog: `--> .calli32_i32i32i32i32_v(18,1,2,4,8)
	==> env.invoke_viiii(index=18,a1=1,a2=2,a3=4,a4=8)
		--> .i32i32i32i32_v(1,2,4,8)
		<--
	<==
<--
`,
		},
	}

	for _, tt := range tests {
		tc := tt

		t.Run(tc.name, func(t *testing.T) {
			defer log.Reset()

			params := tc.params
			params = append([]uint64{uint64(tc.tableOffset)}, params...)

			results, err := mod.ExportedFunction(tc.funcName).Call(testCtx, params...)
			require.NoError(t, err)
			require.Equal(t, tc.expectedResults, results)

			// We expect to see the dynamic function call target
			require.Equal(t, tc.expectedLog, log.String())

			// We expect an unreachable function to err
			params[0]++
			_, err = mod.ExportedFunction(tc.funcName).Call(testCtx, params...)
			require.Error(t, err)
		})
	}
}
