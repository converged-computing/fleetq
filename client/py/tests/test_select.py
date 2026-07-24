import asyncio
import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(__file__))))
from fleetq.io import load_manifests
from fleetq.jobspec import save_jobspecs
from fleetq.requires import derive_requires, is_gpu
from fleetq.select import SelectorTask


def _manifest(root, ref, app, needed):
    parts = ref.replace(":", "/").split("/")
    d = os.path.join(root, *parts)
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "manifest.json"), "w") as f:
        json.dump({"entry": {"reproduce": {"reference": ref, "digest": ref + "@sha256:x"},
                             "artifacts": [{"application": app, "arch": "amd64",
                                            "needed": needed}]}}, f)


def test_requires_projection_is_deterministic():
    # libfabric -> anyof(efa, infiniband); app -> software; cuda -> gpu (containment, not requires)
    req = derive_requires("LAMMPS", ["libcudart.so.12", "libfabric.so.1", "libmpi.so.40"])
    assert req["software"] == [{"type": "lammps"}], req
    net = req["network"][0]
    assert net["type"] == "anyof" and {"type": "efa"} in net["with"] and {"type": "infiniband"} in net["with"]
    assert "gpu" not in req, "gpu is containment, never a requires subsystem"
    assert is_gpu(["libcudart.so.12"]) and not is_gpu(["libc.so.6"])
    # portable build -> no network requirement
    assert "network" not in derive_requires("LAMMPS", ["libmpi.so.40", "libc.so.6"])
    print("OK requires: software from app, network anyof from fabric libs, gpu excluded")


class ChooseGPU:
    async def converse(self, task):
        return {}

    async def run_agent(self, system_prompt, user_prompt, tools, confirm_fn):
        t = {x.name: x for x in tools}
        assert json.loads((await t["list_applications"].handler({}))["content"][0]["text"])
        json.loads((await t["get_variants"].handler({"application": "LAMMPS"}))["content"][0]["text"])
        json.loads((await t["list_clusters"].handler({}))["content"][0]["text"])
        # GPUs on a non-GPU build must be refused
        bad = json.loads((await t["record_jobspec"].handler(
            {"application": "LAMMPS", "reference": "ghcr.io/cc/metric-lammps-cpu:zen4",
             "command": ["lmp"], "nodes": 1, "cores_per_node": 8, "gpus_per_node": 4}))["content"][0]["text"])
        assert "error" in bad and "not GPU-linked" in bad["error"], bad
        # choose the GPU+libfabric build
        await t["record_jobspec"].handler(
            {"application": "LAMMPS", "reference": "ghcr.io/cc/metric-lammps-gpu:libfabric",
             "command": ["lmp", "-in", "in.lj", "-sf", "gpu"], "nodes": 6,
             "cores_per_node": 32, "gpus_per_node": 4, "duration_s": 3600,
             "reasoning": "GPU build linking libcudart; libfabric fits fabric clusters"})
        return None


def test_selector_end_to_end():
    with tempfile.TemporaryDirectory() as d:
        mdir = os.path.join(d, "manifests")
        _manifest(mdir, "ghcr.io/cc/metric-lammps-cpu:zen4", "LAMMPS", ["libmpi.so.40", "libc.so.6"])
        _manifest(mdir, "ghcr.io/cc/metric-lammps-gpu:libfabric", "LAMMPS",
                  ["libcudart.so.12", "libfabric.so.1", "libmpi.so.40"])
        clusters = os.path.join(d, "clusters.json")
        with open(clusters, "w") as f:
            json.dump([{"name": "efa-flux", "manager": "flux", "nodes": 5,
                        "subsystems": ["containment", "software", "network"],
                        "capabilities": ["lammps", "efa"]}], f)

        assert set(load_manifests(mdir)) == {"LAMMPS"}
        jobs = asyncio.run(SelectorTask().execute(ChooseGPU(),
                    {"manifests_dir": mdir, "clusters": clusters, "goal": "gpu lammps"},
                    lambda n, a: True))
        assert len(jobs) == 1
        js = jobs[0].jobspec
        assert js["attributes"]["user"]["image"] == "ghcr.io/cc/metric-lammps-gpu:libfabric"
        # GPU as countable containment
        slot = js["resources"][0]["with"][0]["with"]
        assert {"type": "gpu", "count": 4} in slot and {"type": "core", "count": 32} in slot
        # requires: software + network anyof (derived, not typed)
        assert js["requires"]["software"] == [{"type": "lammps"}]
        assert js["requires"]["network"][0]["type"] == "anyof"
        paths = save_jobspecs(jobs, os.path.join(d, "jobspecs"))
        assert json.load(open(paths[0]))["provenance"]["chosen_reference"].endswith("gpu:libfabric")
        print("OK selector: image + requires from manifest, gpu as containment, gpu-on-cpu refused")


if __name__ == "__main__":
    test_requires_projection_is_deterministic()
    test_selector_end_to_end()
    print("\nall fleetq-select tests passed")
