"""Deterministic projection from a container's RAW facts to fleetq `requires`
sections. The agent chooses the CONTAINER; the requires follow un-pollutably from
that container's linked libraries — the agent never hand-writes requires.

Vertex TYPES here must match the capability vocabulary your clusters register
(datagen `--caps`: software app names + network efa/infiniband/ethernet). Adjust
the tables if your registration uses other names.
"""

from __future__ import annotations

import re

from .jobspec import anyof

# GPU linkage -> the container is GPU-capable (requested as countable containment
# gpu vertices, NOT a requires subsystem).
GPU_MARKERS = ("libcudart", "libcuda", "libamdhip", "libhip", "librocm")

# fabric transport lib -> the network capabilities it can drive. libfabric is
# provider-agnostic (efa or verbs), so it yields an anyof of both.
FABRIC_MARKERS: list[tuple[str, list[str]]] = [
    ("libefa", ["efa"]),
    ("libfabric", ["efa", "infiniband"]),
    ("libibverbs", ["infiniband"]),
    ("libmlx", ["infiniband"]),
    ("librdmacm", ["infiniband"]),
]


def is_gpu(needed: list[str]) -> bool:
    j = [n.lower() for n in needed]
    return any(any(m in n for m in GPU_MARKERS) for n in j)


def software_type(application: str) -> str:
    """App name as a software capability type; trailing version digits stripped
    (amg2023 -> amg) to match app-level cluster caps."""
    return re.sub(r"\d+$", "", application.strip().lower())


def network_options(needed: list[str]) -> list[str]:
    j = [n.lower() for n in needed]
    opts: list[str] = []
    for marker, caps in FABRIC_MARKERS:
        if any(marker in n for n in j):
            for c in caps:
                if c not in opts:
                    opts.append(c)
    return opts


def derive_requires(application: str, needed: list[str]) -> dict:
    """{software:[...], network:[...]} from raw facts. GPUs are NOT here — they
    are countable containment resources on the jobspec."""
    req: dict[str, list] = {}
    sw = software_type(application)
    if sw:
        req["software"] = [{"type": sw}]
    nets = network_options(needed)
    if nets:
        req["network"] = [anyof(nets)] if len(nets) > 1 else [{"type": nets[0]}]
    return req
