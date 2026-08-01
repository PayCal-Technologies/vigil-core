package workflow

import (
	"testing"

	"github.com/PayCal-Technologies/vigil-public/internal/config"
)

func TestFilterByTagIncludesTransitiveDependenciesInDeclarationOrder(t *testing.T) {
	gates := []config.Gate{
		{Name: "generate", Command: "true", ReadOnly: true},
		{Name: "lint", Command: "true", ReadOnly: true, DependsOn: []string{"generate"}},
		{Name: "test", Command: "true", ReadOnly: true, Tags: []string{"pre-push"}, DependsOn: []string{"lint"}},
		{Name: "unrelated", Command: "true", ReadOnly: true},
	}
	filtered, err := FilterByTag(gates, "pre-push")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 3 || filtered[0].Name != "generate" || filtered[1].Name != "lint" || filtered[2].Name != "test" {
		t.Fatalf("filtered gates = %#v", filtered)
	}
}

func TestSchedulerRunsOnlyExplicitParallelGroupsConcurrently(t *testing.T) {
	gates := []config.Gate{
		{Name: "first", Command: "true", ReadOnly: true},
		{Name: "lint", Command: "true", ReadOnly: true, DependsOn: []string{"first"}, ParallelGroup: "analysis"},
		{Name: "test", Command: "true", ReadOnly: true, DependsOn: []string{"first"}, ParallelGroup: "analysis"},
		{Name: "package", Command: "true", ReadOnly: true, DependsOn: []string{"lint", "test"}},
	}
	scheduler, err := NewScheduler(gates, 8)
	if err != nil {
		t.Fatal(err)
	}
	batch, skipped, done := scheduler.Next()
	if done || len(skipped) != 0 || !equalIndexes(batch, []int{0}) {
		t.Fatalf("first decision = batch %v, skipped %#v, done %t", batch, skipped, done)
	}
	if err := scheduler.MarkSucceeded(0); err != nil {
		t.Fatal(err)
	}
	batch, skipped, done = scheduler.Next()
	if done || len(skipped) != 0 || !equalIndexes(batch, []int{1, 2}) {
		t.Fatalf("parallel decision = batch %v, skipped %#v, done %t", batch, skipped, done)
	}
	if err := scheduler.MarkSucceeded(1); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.MarkSucceeded(2); err != nil {
		t.Fatal(err)
	}
	batch, skipped, done = scheduler.Next()
	if done || len(skipped) != 0 || !equalIndexes(batch, []int{3}) {
		t.Fatalf("final decision = batch %v, skipped %#v, done %t", batch, skipped, done)
	}
	if err := scheduler.MarkSucceeded(3); err != nil {
		t.Fatal(err)
	}
	batch, skipped, done = scheduler.Next()
	if !done || len(batch) != 0 || len(skipped) != 0 {
		t.Fatalf("completion decision = batch %v, skipped %#v, done %t", batch, skipped, done)
	}
}

func TestSchedulerPropagatesDependencyFailureAndKeepsIndependentWorkReady(t *testing.T) {
	gates := []config.Gate{
		{Name: "failed", Command: "false", ReadOnly: true, ContinueOnError: true},
		{Name: "dependent", Command: "true", ReadOnly: true, DependsOn: []string{"failed"}},
		{Name: "transitive", Command: "true", ReadOnly: true, DependsOn: []string{"dependent"}},
		{Name: "independent", Command: "true", ReadOnly: true},
	}
	scheduler, err := NewScheduler(gates, 4)
	if err != nil {
		t.Fatal(err)
	}
	batch, _, _ := scheduler.Next()
	if !equalIndexes(batch, []int{0}) {
		t.Fatalf("first batch = %v", batch)
	}
	if err := scheduler.MarkFailed(0); err != nil {
		t.Fatal(err)
	}
	batch, skipped, done := scheduler.Next()
	if done || !equalIndexes(batch, []int{3}) || len(skipped) != 2 {
		t.Fatalf("decision = batch %v, skipped %#v, done %t", batch, skipped, done)
	}
	if skipped[0].Index != 1 || skipped[1].Index != 2 {
		t.Fatalf("skip order = %#v", skipped)
	}
}

func TestSchedulerBoundsParallelGroup(t *testing.T) {
	gates := []config.Gate{
		{Name: "one", Command: "true", ReadOnly: true, ParallelGroup: "checks"},
		{Name: "two", Command: "true", ReadOnly: true, ParallelGroup: "checks"},
		{Name: "three", Command: "true", ReadOnly: true, ParallelGroup: "checks"},
	}
	scheduler, err := NewScheduler(gates, 2)
	if err != nil {
		t.Fatal(err)
	}
	batch, _, _ := scheduler.Next()
	if !equalIndexes(batch, []int{0, 1}) {
		t.Fatalf("batch = %v", batch)
	}
}

func TestSchedulerStateTransitionsFailClosedInsteadOfPanicking(t *testing.T) {
	scheduler, err := NewScheduler([]config.Gate{{Name: "one", Command: "true", ReadOnly: true}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.MarkSucceeded(1); err == nil {
		t.Fatal("expected out-of-range transition error")
	}
	if err := scheduler.MarkSucceeded(0); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.MarkFailed(0); err == nil {
		t.Fatal("expected duplicate transition error")
	}
}

func equalIndexes(actual, expected []int) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
