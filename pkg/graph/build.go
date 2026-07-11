package graph

import (
	"fmt"
	"os"
	"path/filepath"
)

// Builders that EXPORT JGF in the real flux-sched schema (matching tiny.json:
// cluster -> rack -> node -> socket -> core, plus gpu/memory), so the exact same
// files load into real Fluxion via the reapi bindings AND back the dev matcher.
// Capabilities (software, network) are carried as VERTEX PROPERTIES on the
// cluster vertex and matched by Flux jobspec constraints (RFC 31 key-presence) —
// the real Fluxion mechanism, not a separate hand-rolled subsystem graph.

// NodeSpec is one group of identical nodes (homogeneous cluster = one group).
type NodeSpec struct {
	Count int `json:"count"`
	Cores int `json:"cores"`
	GPUs  int `json:"gpus,omitempty"`
	MemGB int `json:"mem,omitempty"`
}

type jgfBuilder struct {
	g      *JGF
	next   int // uniq_id / node id counter
	nodeID int
}

func (b *jgfBuilder) add(v Vertex) string {
	id := fmt.Sprintf("%d", b.next)
	v.ID = id
	v.Metadata.UniqID = b.next
	b.next++
	b.g.Graph.Nodes = append(b.g.Graph.Nodes, v)
	return id
}

func (b *jgfBuilder) edge(src, dst string) {
	b.g.Graph.Edges = append(b.g.Graph.Edges, Edge{Source: src, Target: dst, Metadata: EdgeMeta{Subsystem: ContainmentSubsystem, Name: "contains"}})
}

func meta(t, base, name string, id int, path string) VertexMeta {
	return VertexMeta{Type: t, Basename: base, Name: name, ID: id, Rank: -1, Size: 1, Unit: "",
		Paths: map[string]string{ContainmentSubsystem: path}}
}

// BuildContainment emits a real-schema containment JGF for a cluster. caps are
// capability names (e.g. "lammps","efa") attached as properties on the cluster
// vertex; manager/handle also live there so the loader can recover them.
func BuildContainment(clusterID string, m ManagerType, handle string, groups []NodeSpec, caps []string) *JGF {
	b := &jgfBuilder{g: &JGF{}}
	props := map[string]string{"manager": string(m), "handle": handle}
	for _, c := range caps {
		props[c] = "" // RFC 31: key-presence; value empty
	}
	root := meta("cluster", clusterID, clusterID, 0, "/"+clusterID)
	root.Properties = props
	rootID := b.add(Vertex{Metadata: root})
	rackPath := "/" + clusterID + "/rack0"
	rackID := b.add(Vertex{Metadata: meta("rack", "rack", "rack0", 0, rackPath)})
	b.edge(rootID, rackID)

	for _, grp := range groups {
		for i := 0; i < grp.Count; i++ {
			npath := fmt.Sprintf("%s/node%d", rackPath, b.nodeID)
			nm := meta("node", "node", fmt.Sprintf("node%d", b.nodeID), b.nodeID, npath)
			nm.Rank = b.nodeID
			// Capabilities live on the node vertices too: Fluxion matches
			// jobspec constraint properties against the vertices actually
			// selected, not ancestors, so node-level placement is required.
			if len(caps) > 0 {
				nm.Properties = map[string]string{}
				for _, c := range caps {
					nm.Properties[c] = ""
				}
			}
			nID := b.add(Vertex{Metadata: nm})
			b.edge(rackID, nID)
			// socket -> cores
			spath := npath + "/socket0"
			sID := b.add(Vertex{Metadata: meta("socket", "socket", "socket0", 0, spath)})
			b.edge(nID, sID)
			for c := 0; c < grp.Cores; c++ {
				cm := meta("core", "core", fmt.Sprintf("core%d", c), c, fmt.Sprintf("%s/core%d", spath, c))
				cID := b.add(Vertex{Metadata: cm})
				b.edge(sID, cID)
			}
			for gp := 0; gp < grp.GPUs; gp++ {
				gm := meta("gpu", "gpu", fmt.Sprintf("gpu%d", gp), gp, fmt.Sprintf("%s/gpu%d", npath, gp))
				gID := b.add(Vertex{Metadata: gm})
				b.edge(nID, gID)
			}
			if grp.MemGB > 0 {
				mm := meta("memory", "memory", "memory0", 0, npath+"/memory0")
				mm.Size = grp.MemGB
				mm.Unit = "GB"
				mID := b.add(Vertex{Metadata: mm})
				b.edge(nID, mID)
			}
			b.nodeID++
		}
	}
	return b.g
}

// ExportCluster writes the containment JGF for a cluster under root/<id>/.
func ExportCluster(root, clusterID string, containment *JGF) error {
	dir := filepath.Join(root, clusterID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeJGF(filepath.Join(dir, ContainmentSubsystem+".json"), containment)
}

// ClusterSpec is the registration payload for adding a cluster at runtime (API
// or CLI). It compiles to a real containment JGF via BuildContainment.
type ClusterSpec struct {
	Name         string      `json:"name"`
	Manager      ManagerType `json:"manager"`
	Handle       string      `json:"handle,omitempty"`
	Nodes        []NodeSpec  `json:"nodes"`
	Capabilities []string    `json:"capabilities,omitempty"`
}

// Build compiles the spec into a ClusterGraph (in-memory; no files).
func (s ClusterSpec) Build() (ClusterGraph, error) {
	if s.Name == "" {
		return ClusterGraph{}, fmt.Errorf("cluster name is required")
	}
	if len(s.Nodes) == 0 {
		return ClusterGraph{}, fmt.Errorf("cluster %q needs at least one node group", s.Name)
	}
	if s.Manager == "" {
		return ClusterGraph{}, fmt.Errorf("cluster %q needs a manager type", s.Name)
	}
	jgf := BuildContainment(s.Name, s.Manager, s.Handle, s.Nodes, s.Capabilities)
	return ClusterFromContainment(jgf)
}

// ClusterFromContainment builds a ClusterGraph from an in-memory containment
// JGF, recovering id/manager/handle from the cluster root vertex properties.
func ClusterFromContainment(jgf *JGF) (ClusterGraph, error) {
	cg := ClusterGraph{Subsystems: map[string]*JGF{ContainmentSubsystem: jgf}}
	root := jgf.find(func(v Vertex) bool { return v.Metadata.Type == "cluster" })
	if root == nil {
		return ClusterGraph{}, fmt.Errorf("containment has no cluster root vertex")
	}
	cg.ID = firstNonEmpty(root.Metadata.Name, root.Metadata.Basename)
	cg.Manager = ManagerType(root.Metadata.Properties["manager"])
	cg.Handle = root.Metadata.Properties["handle"]
	return cg, nil
}
