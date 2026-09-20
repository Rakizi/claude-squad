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

⛔ AND THE UNMUTATED TREE IS PROVED GREEN FIRST. Without that, "the mutant was
detected" and "the suite was already red" are the same non-zero exit -- the
exact collapse this branch spent five rounds removing from kill and pause,
reinstated in the tool that certifies them. MEASURED: with one unrelated failing
test in the tree, a mutant that CHANGES ZERO BYTES was reported RED and the
harness exited 0. The flake this branch just fixed ran at ~9% per run and this
harness mutates 7 files, so roughly HALF of all runs had at least one free RED.

Exits follow the house contract: 0 every mutant died · 1 something SURVIVED ·
3 COULD NOT LOOK (the baseline is red, or a mutant could not be applied). ⛔
INVALID is a could-not-look, not a finding -- collapsing it into 1 is the same
defect one more layer down.
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
    ("N6", "app/app.go", "\treturn m.storage.SyncInstances(m.list.GetInstances())",
     "\treturn m.storage.SyncInstances(nil)",
     "the fallback drops the list and writes the old file back verbatim"),
    #: The hoist that shipped GREEN until round 5 -- moved above the Pause
    #: CALL, not above the proof. The refusal test stops at prePauseProof, so
    #: target.Pause() never runs and only a seam can reach this.
    #: ⚠ A MOVE, NOT A DUPLICATE. Inserting a second call and leaving the first
    #: models a build that closes the pane TWICE, which is not the defect. The
    #: anchor spans from the real call to the Pause call so the replacement can
    #: delete one and insert the other in a single edit.
    ("N4", "pause.go",
     "\tcloseTerminalSession(title)\n\n\t// \u26d4 Pause() only mutates",
     "\t// \u26d4 Pause() only mutates",
     "the pane is never closed on a successful pause (half of the move)"),
    ("N4b", "pause.go", "\tif err := pauseOp(target); err != nil {",
     "\tcloseTerminalSession(title)\n\tif err := pauseOp(target); err != nil {",
     "a failed pause closes the pane it said it left alone"),
]


def sh(*cmd, cwd):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True).returncode


def main(argv):
    want = set(argv[1:])
    rc = 0

    #: ⛔ THE BASELINE, BEFORE ANY MUTATION. A red tree makes every mutant look
    #: killed, for free.
    with tempfile.TemporaryDirectory() as tmp:
        base = pathlib.Path(tmp) / "base"
        shutil.copytree(ROOT, base, ignore=shutil.ignore_patterns(".git"))
        if sh("go", "build", "./...", cwd=base) != 0:
            print("  COULD NOT LOOK  the unmutated tree does not BUILD")
            return 3
        if sh("go", "test", "./...", "-count=1", cwd=base) != 0:
            print("  COULD NOT LOOK  the unmutated tree is not green -- every RED "
                  "below would be free. Fix the suite first.")
            return 3
    print("  baseline: build and go test ./... GREEN before the first mutation")
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
                rc = max(rc, 3)
                continue
            mutated = src.replace(before, after, 1)
            if mutated == src:
                #: before == after changes nothing, so a RED would be the
                #: baseline's, not the mutant's.
                print("  INVALID   %-5s mutates ZERO BYTES" % name)
                rc = max(rc, 3)
                continue
            f.write_text(mutated)
            if sh("go", "build", "./...", cwd=tree) != 0:
                print("  INVALID   %-5s does not compile -- proves an unused "
                      "identifier, not a behavioural guard" % name)
                rc = max(rc, 3)
                continue
            if sh("go", "vet", "./...", cwd=tree) != 0:
                print("  INVALID   %-5s does not vet" % name)
                rc = max(rc, 3)
                continue
            red = sh("go", "test", "./...", "-count=1", cwd=tree) != 0
            print("  %-9s %-5s %s" % ("RED" if red else "SURVIVED", name, reinstates))
            if not red:
                rc = max(rc, 1)
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv))
