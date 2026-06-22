package contexty_test

import (
	"testing"
)

// Allocation guardrails for hot paths. Thresholds include headroom
// for CI variance; tighten when optimizing benchmarks.
const (
	maxAllocsRender    = 400
	maxAllocsRedaction = 750
	maxAllocsTruncate  = 550
)

func TestBenchGuardrails_Render(t *testing.T) {
	// Act.
	result := testing.Benchmark(BenchmarkRender_LLMXML)
	// Assert.
	if result.AllocsPerOp() > maxAllocsRender {
		t.Fatalf("render allocs/op %d exceeds guardrail %d", result.AllocsPerOp(), maxAllocsRender)
	}
}

func TestBenchGuardrails_Redaction(t *testing.T) {
	// Act.
	result := testing.Benchmark(BenchmarkRedactionHook)
	// Assert.
	if result.AllocsPerOp() > maxAllocsRedaction {
		t.Fatalf("redaction allocs/op %d exceeds guardrail %d", result.AllocsPerOp(), maxAllocsRedaction)
	}
}

func TestBenchGuardrails_Truncate(t *testing.T) {
	// Act.
	result := testing.Benchmark(BenchmarkBudgetPipeline_Truncate)
	// Assert.
	if result.AllocsPerOp() > maxAllocsTruncate {
		t.Fatalf("truncate allocs/op %d exceeds guardrail %d", result.AllocsPerOp(), maxAllocsTruncate)
	}
}
