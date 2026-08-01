package workflow

import (
	"fmt"
	"strings"

	"github.com/PayCal-Technologies/vigil-public/internal/config"
)

type NodeState string

const (
	NodePending           NodeState = "pending"
	NodeSucceeded         NodeState = "succeeded"
	NodeFailed            NodeState = "failed"
	NodeDependencySkipped NodeState = "dependency_skipped"
	NodeHalted            NodeState = "halted"
)

type Skip struct {
	Index              int
	FailedDependencies []string
	State              NodeState
}

type Scheduler struct {
	gates       []config.Gate
	nameToIndex map[string]int
	states      []NodeState
	maxParallel int
}

func NewScheduler(gates []config.Gate, maxParallel int) (*Scheduler, error) {
	if issues := config.GateIssues(gates); len(issues) > 0 {
		return nil, fmt.Errorf("invalid workflow graph: %s", strings.Join(config.IssueMessages(issues), "; "))
	}
	if maxParallel < 1 {
		maxParallel = 1
	}
	nameToIndex := make(map[string]int, len(gates))
	for index, gate := range gates {
		nameToIndex[gate.Name] = index
	}
	states := make([]NodeState, len(gates))
	for index := range states {
		states[index] = NodePending
	}
	return &Scheduler{
		gates:       cloneGates(gates),
		nameToIndex: nameToIndex,
		states:      states,
		maxParallel: maxParallel,
	}, nil
}

func FilterByTag(gates []config.Gate, tag string) ([]config.Gate, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return cloneGates(gates), nil
	}
	if issues := config.GateIssues(gates); len(issues) > 0 {
		return nil, fmt.Errorf("invalid workflow graph: %s", strings.Join(config.IssueMessages(issues), "; "))
	}
	nameToIndex := make(map[string]int, len(gates))
	for index, gate := range gates {
		nameToIndex[gate.Name] = index
	}
	selected := make([]bool, len(gates))
	var includeDependencies func(int)
	includeDependencies = func(index int) {
		if selected[index] {
			return
		}
		selected[index] = true
		for _, dependency := range gates[index].DependsOn {
			includeDependencies(nameToIndex[dependency])
		}
	}
	for index, gate := range gates {
		for _, candidate := range gate.Tags {
			if candidate == tag {
				includeDependencies(index)
				break
			}
		}
	}
	filtered := make([]config.Gate, 0, len(gates))
	for index, gate := range gates {
		if selected[index] {
			filtered = append(filtered, gate)
		}
	}
	return cloneGates(filtered), nil
}

func (scheduler *Scheduler) Next() (batch []int, skipped []Skip, done bool) {
	for {
		changed := false
		for index, state := range scheduler.states {
			if state != NodePending {
				continue
			}
			failed := scheduler.failedDependencies(index)
			if len(failed) == 0 {
				continue
			}
			scheduler.states[index] = NodeDependencySkipped
			skipped = append(skipped, Skip{
				Index:              index,
				FailedDependencies: failed,
				State:              NodeDependencySkipped,
			})
			changed = true
		}
		if !changed {
			break
		}
	}

	ready := make([]int, 0, len(scheduler.gates))
	for index, state := range scheduler.states {
		if state == NodePending && scheduler.dependenciesSucceeded(index) {
			ready = append(ready, index)
		}
	}
	if len(ready) > 0 {
		first := ready[0]
		group := scheduler.gates[first].ParallelGroup
		batch = append(batch, first)
		if group != "" {
			for _, index := range ready[1:] {
				if len(batch) >= scheduler.maxParallel {
					break
				}
				if scheduler.gates[index].ParallelGroup == group {
					batch = append(batch, index)
				}
			}
		}
		return batch, skipped, false
	}
	for _, state := range scheduler.states {
		if state == NodePending {
			return nil, skipped, false
		}
	}
	return nil, skipped, true
}

func (scheduler *Scheduler) MarkSucceeded(index int) error {
	return scheduler.setState(index, NodeSucceeded)
}

func (scheduler *Scheduler) MarkFailed(index int) error {
	return scheduler.setState(index, NodeFailed)
}

func (scheduler *Scheduler) Halt() []Skip {
	skipped := make([]Skip, 0)
	for index, state := range scheduler.states {
		if state != NodePending {
			continue
		}
		scheduler.states[index] = NodeHalted
		skipped = append(skipped, Skip{Index: index, State: NodeHalted})
	}
	return skipped
}

func (scheduler *Scheduler) State(index int) NodeState {
	if index < 0 || index >= len(scheduler.states) {
		return ""
	}
	return scheduler.states[index]
}

func (scheduler *Scheduler) setState(index int, state NodeState) error {
	if index < 0 || index >= len(scheduler.states) {
		return fmt.Errorf("workflow scheduler index %d out of range", index)
	}
	if scheduler.states[index] != NodePending {
		return fmt.Errorf("workflow scheduler node %d already completed with state %s", index, scheduler.states[index])
	}
	scheduler.states[index] = state
	return nil
}

func (scheduler *Scheduler) failedDependencies(index int) []string {
	failed := make([]string, 0)
	for _, dependency := range scheduler.gates[index].DependsOn {
		state := scheduler.states[scheduler.nameToIndex[dependency]]
		if state == NodeFailed || state == NodeDependencySkipped || state == NodeHalted {
			failed = append(failed, dependency)
		}
	}
	return failed
}

func (scheduler *Scheduler) dependenciesSucceeded(index int) bool {
	for _, dependency := range scheduler.gates[index].DependsOn {
		if scheduler.states[scheduler.nameToIndex[dependency]] != NodeSucceeded {
			return false
		}
	}
	return true
}

func cloneGates(gates []config.Gate) []config.Gate {
	cloned := make([]config.Gate, len(gates))
	for index, gate := range gates {
		cloned[index] = gate
		cloned[index].DependsOn = append([]string(nil), gate.DependsOn...)
	}
	return cloned
}
