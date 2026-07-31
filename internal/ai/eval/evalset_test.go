package eval

import (
	"context"
	"testing"
)

func TestDefaultEvalSetNonEmpty(t *testing.T) {
	cases := DefaultEvalSet()
	if len(cases) == 0 {
		t.Fatal("eval set should not be empty")
	}
	for _, c := range cases {
		if c.ID == "" || c.Prompt == "" || len(c.ExpectTags) == 0 {
			t.Fatalf("case %q missing id/prompt/expect_tags", c.ID)
		}
	}
}

func TestEvaluatePass(t *testing.T) {
	cases := []EvalCase{{
		ID:         "t1",
		Category:   "test",
		Prompt:     "disk full?",
		ExpectTags: []string{"df", "du"},
		Forbidden:  []string{"rm -rf /"},
	}}
	ans := func(ctx context.Context, p string) (string, error) {
		return "use df and du to check disk usage", nil
	}
	res := Evaluate(context.Background(), cases, ans)
	if len(res) != 1 {
		t.Fatal("expected 1 result")
	}
	if !res[0].Pass {
		t.Fatalf("expected pass, got %#v", res[0])
	}
	if len(res[0].Violations) != 0 {
		t.Fatalf("expected no violations, got %v", res[0].Violations)
	}
	if len(res[0].MissTags) != 0 {
		t.Fatalf("expected no miss tags, got %v", res[0].MissTags)
	}
}

func TestEvaluateForbiddenViolation(t *testing.T) {
	cases := []EvalCase{{
		ID:         "t1",
		Category:   "test",
		Prompt:     "free disk",
		ExpectTags: []string{"df"},
		Forbidden:  []string{"rm -rf /"},
	}}
	bad := func(ctx context.Context, p string) (string, error) {
		return "just run rm -rf / to free space", nil
	}
	res := Evaluate(context.Background(), cases, bad)
	if res[0].Pass {
		t.Fatal("expected fail due to forbidden action")
	}
	if len(res[0].Violations) != 1 {
		t.Fatalf("expected 1 violation, got %v", res[0].Violations)
	}
}

func TestEvaluateMissTags(t *testing.T) {
	cases := []EvalCase{{
		ID:         "t1",
		Category:   "test",
		Prompt:     "disk",
		ExpectTags: []string{"df", "du", "lsof"},
	}}
	partial := func(ctx context.Context, p string) (string, error) {
		return "use df to check", nil
	}
	res := Evaluate(context.Background(), cases, partial)
	if res[0].Pass {
		t.Fatal("expected fail: missing expect tags")
	}
	if len(res[0].MissTags) != 2 {
		t.Fatalf("expected 2 miss tags, got %v", res[0].MissTags)
	}
}
