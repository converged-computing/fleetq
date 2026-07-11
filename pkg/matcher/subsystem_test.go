package matcher_test

import (
	"encoding/json"
	"testing"

	"github.com/converged-computing/fleetq/pkg/graph"
	"github.com/converged-computing/fleetq/pkg/jobspec"
	"github.com/converged-computing/fleetq/pkg/matcher"
)

// software subsystem tree: lammps -> kokkos  (no mpi vertex anywhere).
const swTree = `{"graph":{"nodes":[
 {"id":"1","metadata":{"type":"software","basename":"lammps","name":"lammps","id":0,"uniq_id":1,"size":1,"paths":{"software":"/lammps"}}},
 {"id":"2","metadata":{"type":"software","basename":"kokkos","name":"kokkos","id":0,"uniq_id":2,"size":1,"paths":{"software":"/lammps/kokkos"}}}
],"edges":[
 {"source":"1","target":"2","metadata":{"subsystem":"software","name":"depends"}}
]}}`

func oneNodeCluster(t *testing.T, id string) graph.ClusterGraph {
	t.Helper()
	cg, err := graph.ClusterSpec{
		Name: id, Manager: "flux-operator",
		Nodes: []graph.NodeSpec{{Count: 1, Cores: 8}},
	}.Build()
	if err != nil {
		t.Fatal(err)
	}
	return cg
}

func need(sub string, section []jobspec.Resource) jobspec.Jobspec {
	return jobspec.New("j", "", nil, 1, 1, 0, map[string][]jobspec.Resource{sub: section})
}

func feasibleOn(m matcher.Matcher, js jobspec.Jobspec, cluster string) bool {
	for _, c := range m.Evaluate(js) {
		if c.Cluster == cluster {
			return c.Feasible
		}
	}
	return false
}

// The dependency must match structurally: lammps WITH kokkos satisfies the
// lammps->kokkos tree, but lammps WITH mpi does not (mpi is absent). This is the
// exact gate Fluxion's reapi got wrong; the sim is strict by construction.
func TestSubsystemSubtreeGate(t *testing.T) {
	m := matcher.NewSim(nil)
	if err := m.AddCluster(oneNodeCluster(t, "c1")); err != nil {
		t.Fatal(err)
	}
	var tree graph.JGF
	if err := json.Unmarshal([]byte(swTree), &tree); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSubsystem("c1", "software", &tree, true); err != nil {
		t.Fatal(err)
	}

	lammpsKokkos := need("software", []jobspec.Resource{
		{Type: "lammps", With: []jobspec.Resource{{Type: "kokkos"}}},
	})
	if !feasibleOn(m, lammpsKokkos, "c1") {
		t.Fatal("lammps WITH kokkos should satisfy the lammps->kokkos tree")
	}

	lammpsMPI := need("software", []jobspec.Resource{
		{Type: "lammps", With: []jobspec.Resource{{Type: "mpi"}}},
	})
	if feasibleOn(m, lammpsMPI, "c1") {
		t.Fatal("lammps WITH mpi must NOT satisfy (mpi absent from the tree)")
	}
}

// A descriptive subsystem gates feasibility but is never consumed: Satisfy
// allocates nothing, and Allocate only touches containment.
func TestDescriptiveSubsystemNotConsumed(t *testing.T) {
	m := matcher.NewSim(nil)
	if err := m.AddCluster(oneNodeCluster(t, "c1")); err != nil {
		t.Fatal(err)
	}
	var tree graph.JGF
	_ = json.Unmarshal([]byte(swTree), &tree)
	_ = m.AddSubsystem("c1", "software", &tree, true)

	js := need("software", []jobspec.Resource{{Type: "lammps"}})

	// Evaluate twice: side-effect free, so both see the node free.
	if !m.Evaluate(js)[0].FreeNow || !m.Evaluate(js)[0].FreeNow {
		t.Fatal("Evaluate must not consume capacity")
	}
	// Allocate consumes only containment (one node); a second allocate is full.
	alloc, ok, err := m.Allocate(js, "c1")
	if err != nil || !ok {
		t.Fatalf("allocate failed: ok=%v err=%v", ok, err)
	}
	if len(alloc.VertexIDs) != 1 {
		t.Fatalf("expected 1 containment vertex consumed, got %d", len(alloc.VertexIDs))
	}
	if _, ok, _ := m.Allocate(js, "c1"); ok {
		t.Fatal("second allocate should be full (one node)")
	}
}
