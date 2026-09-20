#!/usr/bin/env python3
"""Apply each named mutant, run build+vet+test, report RED or SURVIVED.

⛔ THIS EXISTS BECAUSE A COMMIT MESSAGE CLAIMED A MUTANT WENT RED THREE ROUNDS
RUNNING AND IT DID NOT. Each time the claim came from running something ADJACENT
to the named mutant -- gutting a different fallback, hoisting past a different
call -- and reporting it under the name of the one that was never run. Two
audits also reported they could not recover the definitions to re-run them,
because the definitions lived only in a shell loop in a transcript.

⭐ So the definitions live HERE, in the repo, and the claim is a command anyone
can re-run rather than a sentence anyone has to trust.

    python3 mutants.py           every mutant
    python3 mutants.py M11 N4    just those

A mutant that does not COMPILE is reported INVALID, never RED: a build failure
proves an unused identifier, not a test that detects behaviour.
"""
import pathlib
import shutil
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent

#: (name, file, before, after, what it reinstates if it survives)
MUTANTS = [
    ("M4", "kill.go", "\tcloseTerminalSession(title)", "\t_ = closeTerminalSession",
     "kill orphans the term_ session again"),
    ("M4b", "pause.go", "\tcloseTerminalSession(title)", "\t_ = closeTerminalSession",
     "pause orphans the term_ session again"),
    ("M7", "kill.go", "case wt.IsExistingBranch:", "case false:",
     "an existing-branch session is refused forever"),
    ("M8", "kill.go", "case wt.IsExistingBranch:", "case true:",
     "an ordinary session's surviving branch stops being counted"),
    ("M10", "app/app.go", "\treturn m.storage.SyncInstances(m.list.GetInstances())\n}",
     "\treturn nil\n}", "c and r silently lose their write"),
    ("M11", "app/app.go", "\treturn i.Resume()\n}", "\treturn nil\n}",
     "r reports success and never resumes"),
    #: The hoist that shipped GREEN until round 5 -- moved above the Pause
    #: CALL, not above the proof. The refusal test stops at prePauseProof, so
    #: target.Pause() never runs and only a seam can reach this.
    ("N4", "pause.go", "\tif err := pauseOp(target); err != nil {",
     "\tcloseTerminalSession(title)\n\tif err := pauseOp(target); err != nil {",
     "a failed pause closes the pane it said it left alone"),
]


def sh(*cmd, cwd):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True).returncode


def main(argv):
    want = set(argv[1:])
    rc = 0
    for name, rel, before, after, reinstates in MUTANTS:
        if want and name not in want:
            continue
        with tempfile.TemporaryDirectory() as tmp:
            tree = pathlib.Path(tmp) / "t"
            shutil.copytree(ROOT, tree, ignore=shutil.ignore_patterns(".git"))
            f = tree / rel
            src = f.read_text()
            if src.count(before) != 1:
                print("  INVALID   %-5s anchor appears %d times, not once"
                      % (name, src.count(before)))
                rc = 1
                continue
            f.write_text(src.replace(before, after, 1))
            if sh("go", "build", "./...", cwd=tree) != 0:
                print("  INVALID   %-5s does not compile -- proves an unused "
                      "identifier, not a behavioural guard" % name)
                rc = 1
                continue
            if sh("go", "vet", "./...", cwd=tree) != 0:
                print("  INVALID   %-5s does not vet" % name)
                rc = 1
                continue
            red = sh("go", "test", "./...", "-count=1", cwd=tree) != 0
            print("  %-9s %-5s %s" % ("RED" if red else "SURVIVED", name, reinstates))
            if not red:
                rc = 1
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv))
