"""fleetq — Python client for fleetq (ships in the fleetq repo, versioned with it).

Subcommands via `python -m fleetq <cmd>` or dedicated entrypoints. Today: `select`
(author jobspecs by choosing the best container per application, clusters in mind).
"""

from .jobspec import SelectedJob, anyof, build_jobspec, containment, save_jobspecs
from .requires import derive_requires, is_gpu
from .select import SelectorTask

__all__ = ["SelectorTask", "build_jobspec", "containment", "anyof", "SelectedJob",
           "save_jobspecs", "derive_requires", "is_gpu"]
