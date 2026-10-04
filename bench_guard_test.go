package contexty_test

import (
	"testing"
)

// Allocation guardrails for hot paths. Thresholds include headroom
// for CI variance; tighten when optimizing benchmarks.
const (
	maxAllocsRender        = 400
	maxAllocsHostTransform = 750
	maxAllocsTruncate      = 550
)

func TestBenchGuardrails_Render(t *testing.T) {
	// Act.
	result := testing.Benchmark(BenchmarkRender_LLMXML)
	// Assert.
	if result.AllocsPerOp() > maxAllocsRender {
		t.Fatalf("render allocs/op %d exceeds guardrail %d", result.AllocsPerOp(), maxAllocsRender)
	}
}

func TestBenchGuardrails_HostTextTransform(t *testing.T) {
	// Act.
	result := testing.Benchmark(BenchmarkHostTextTransform)
	// Assert.
	if result.AllocsPerOp() > maxAllocsHostTransform {
		t.Fatalf("host transform allocs/op %d exceeds guardrail %d", result.AllocsPerOp(), maxAllocsHostTransform)
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
