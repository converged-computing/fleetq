"""SelectorTask: choose the best container per application and author a fleetq
jobspec, keeping the available clusters in mind. Reads manifests (raw facts) +
the cluster snapshot; the agent picks a container; the harness stamps the image
from the chosen manifest and derives `requires` deterministically from its
linked libraries. It does NOT pick a cluster and does NOT submit.
"""

from __future__ import annotations

import json
from typing import Any

from behalf import AgentRunner, ConfirmFn, Task, ToolSpec

from .io import load_clusters, load_manifests
from .jobspec import SelectedJob, build_jobspec
from .requires import derive_requires, is_gpu

SELECT = """You choose the best container for each application and turn it into a
fleetq jobspec, keeping the available clusters in mind. You do NOT choose a
cluster (a scheduler places it later) — you choose a CONTAINER that could run
well given what clusters exist.

Use list_applications, then for each application use get_variants to see the
candidate containers and their raw facts (arch, linked libraries), and
list_clusters to see what each cluster offers (capabilities like lammps/efa/
infiniband, gpu presence, node counts). Reason from the facts: a build linking
libcudart is GPU-capable and suits a cluster with GPUs; a libfabric/verbs build
wants a fast interconnect; a plain build is portable. Pick the variant that best
serves the goal, then call record_jobspec with its reference, the command, and a
resource request. One jobspec per application. Put your reasoning in `reasoning`.

You do not write the requires/capabilities yourself — those are derived from the
container you choose. You only set node/core/gpu counts and the command."""

SETUP = """You are setting up a selection run. Ask only what you can't infer: the
goal, and optionally which applications to limit to. Then call finalize_setup
with a manifest containing manifests_dir, clusters, goal, and out_dir."""


def _text(obj: Any) -> dict:
    return {"content": [{"type": "text", "text": json.dumps(obj, indent=2)}]}


class SelectorTask(Task):
    name = "select"

    def manifest_schema(self) -> dict:
        return {"manifests_dir": str, "clusters": str, "goal": str,
                "out_dir": str, "duration_s": int}

    def setup_system_prompt(self) -> str:
        return SETUP

    def execute_system_prompt(self, manifest: dict) -> str:
        return SELECT

    def selection_tools(self, catalog, clusters, sink, manifest) -> list[ToolSpec]:
        async def list_applications(a):
            return _text([{"application": k, "variants": len(v)} for k, v in catalog.items()])

        async def get_variants(a):
            return _text(catalog.get(a.get("application", ""), []))

        async def list_clusters(a):
            return _text(clusters)

        async def record_jobspec(a):
            app, ref = a["application"], a["reference"]
            variants = {v["reference"]: v for v in catalog.get(app, [])}
            if ref not in variants:
                return _text({"error": f"{ref!r} is not a profiled variant of {app!r}; "
                                       f"choose one of {list(variants)}"})
            needed = variants[ref].get("needed", [])
            gpus = int(a.get("gpus_per_node", 0))
            if gpus > 0 and not is_gpu(needed):
                return _text({"error": f"requested {gpus} gpus but {ref} is not GPU-linked "
                                       f"(no libcudart/libamdhip in its libraries)"})
            requires = derive_requires(app, needed)  # deterministic from facts
            js = build_jobspec(
                name=app.lower(), image=ref, command=a["command"],
                nodes=a.get("nodes", 1), cores_per_node=a.get("cores_per_node", 1),
                gpus_per_node=gpus, duration_s=a.get("duration_s", manifest.get("duration_s", 3600)),
                requires=requires,
            )
            sink.append(SelectedJob(application=app, jobspec=js, chosen_reference=ref,
                                    reasoning=a.get("reasoning", ""),
                                    alternatives=[r for r in variants if r != ref]))
            return _text(f"recorded jobspec for {app} using {ref} (requires={requires})")

        return [
            ToolSpec("list_applications", "List profiled applications and variant counts.", {}, list_applications),
            ToolSpec("get_variants", "Candidate containers for an application with raw facts.",
                     {"application": str}, get_variants),
            ToolSpec("list_clusters", "Available clusters and their capabilities/nodes.", {}, list_clusters),
            ToolSpec("record_jobspec",
                     "Emit one jobspec: chosen container reference (from get_variants), command, and a "
                     "resource request (nodes, cores_per_node, gpus_per_node, duration_s). Image and "
                     "requires are derived from the manifest; don't type them.",
                     {"application": str, "reference": str, "command": list, "nodes": int,
                      "cores_per_node": int, "gpus_per_node": int, "duration_s": int, "reasoning": str},
                     record_jobspec)]

    async def execute(self, runner: AgentRunner, manifest: dict, confirm_fn: ConfirmFn) -> list[SelectedJob]:
        catalog = load_manifests(manifest["manifests_dir"])
        clusters = load_clusters(manifest.get("clusters"))
        sink: list[SelectedJob] = []
        tools = self.selection_tools(catalog, clusters, sink, manifest)
        await runner.run_agent(
            self.execute_system_prompt(manifest),
            f"Choose containers and author jobspecs. Goal: {manifest.get('goal', 'run each application well')}",
            tools, confirm_fn)
        return sink
