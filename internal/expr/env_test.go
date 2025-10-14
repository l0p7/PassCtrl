package expr

import "testing"

func TestLookupMapValue(t *testing.T) {
	env, err := NewEnvironment()
	if err != nil {
		t.Fatalf("new environment: %v", err)
	}

	program, err := env.Compile(`lookup(forward.headers, "key") == "value"`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	activation := map[string]any{
		"forward": map[string]any{
			"headers": map[string]any{"key": "value"},
		},
	}
	matched, err := program.EvalBool(activation)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !matched {
		t.Fatalf("expected lookup to match existing key")
	}

	missingProgram, err := env.Compile(`lookup(forward.headers, "missing") == "value"`)
	if err != nil {
		t.Fatalf("compile missing: %v", err)
	}
	matched, err = missingProgram.EvalBool(activation)
	if err != nil {
		t.Fatalf("eval missing: %v", err)
	}
	if matched {
		t.Fatalf("expected lookup to return null for missing key")
	}
}

func TestCompileValue(t *testing.T) {
	env, err := NewEnvironment()
	if err != nil {
		t.Fatalf("new environment: %v", err)
	}

	program, err := env.CompileValue(`forward.headers["key"]`)
	if err != nil {
		t.Fatalf("compile value: %v", err)
	}

	activation := map[string]any{
		"forward": map[string]any{
			"headers": map[string]any{"key": "value"},
		},
	}

	result, err := program.Eval(activation)
	if err != nil {
		t.Fatalf("eval value: %v", err)
	}
	if result != "value" {
		t.Fatalf("expected value result, got %v", result)
	}

	if _, err := program.EvalBool(activation); err == nil {
		t.Fatalf("expected EvalBool to fail for non-boolean program")
	}
}
