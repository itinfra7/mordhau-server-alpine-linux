package manager

import "testing"

func configuredModForDependencyTest(
	id int,
	name string,
	dependencies []int,
	checked bool,
) ConfiguredMod {
	items := make([]ModIOItem, 0, len(dependencies))
	for _, dependency := range dependencies {
		items = append(items, ModIOItem{
			ID:   dependency,
			Name: "Dependency",
		})
	}
	return ConfiguredMod{
		ID:      id,
		Enabled: true,
		Metadata: &ModIOItem{
			ID:   id,
			Name: name,
		},
		Dependencies:        items,
		DependenciesChecked: checked,
	}
}

func TestModDependencyGraphAndRemovalPlanRetainSharedEntries(t *testing.T) {
	view := ModManagementView{
		Revision: 42,
		Mods: []ConfiguredMod{
			configuredModForDependencyTest(100, "Target", []int{200, 300}, true),
			configuredModForDependencyTest(200, "Target dependency", nil, true),
			configuredModForDependencyTest(300, "Shared dependency", nil, true),
			configuredModForDependencyTest(400, "Other root", []int{300}, true),
		},
	}
	graph := buildModDependencyGraph(view.Mods)
	var shared *ModDependencyGraphNode
	for index := range graph {
		if graph[index].ID == 300 {
			shared = &graph[index]
			break
		}
	}
	if shared == nil || len(shared.RequiredBy) != 2 ||
		shared.RequiredBy[0] != 100 || shared.RequiredBy[1] != 400 {
		t.Fatalf("unexpected shared dependency graph node: %+v", shared)
	}

	plan, err := buildModRemovalPlan(view, 100)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Revision != 42 || len(plan.Candidates) != 2 ||
		plan.Candidates[0].ID != 100 || plan.Candidates[1].ID != 200 {
		t.Fatalf("unexpected removable candidates: %+v", plan.Candidates)
	}
	if len(plan.RetainedDependencies) != 1 ||
		plan.RetainedDependencies[0].ID != 300 ||
		len(plan.RetainedDependencies[0].RequiredBy) != 1 ||
		plan.RetainedDependencies[0].RequiredBy[0] != 400 {
		t.Fatalf("shared dependency was not retained: %+v", plan.RetainedDependencies)
	}
}

func TestModRemovalPlanIsConservativeWhenAnotherGraphIsUnknown(t *testing.T) {
	view := ModManagementView{
		Mods: []ConfiguredMod{
			configuredModForDependencyTest(100, "Target", []int{200}, true),
			configuredModForDependencyTest(200, "Dependency", nil, true),
			configuredModForDependencyTest(300, "Unknown", nil, false),
		},
	}
	plan, err := buildModRemovalPlan(view, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].ID != 100 ||
		plan.Warning == "" {
		t.Fatalf("unknown dependency graph was not handled conservatively: %+v", plan)
	}
}
