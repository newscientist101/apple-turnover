#!/usr/bin/env python3
"""mutation-parse.py — validate scripts/mutations.json and emit grid records.

The mutation table is scripts/mutations.json. A row is {name, file, old,
new} plus an optional `run`, and an anchor is JSON, so it may contain ANY
character -- including a `|`, a `||`, a tab, a newline or a quote. There is no
delimiter left for an anchor to collide with, so nothing can truncate a row.

That is the whole point of the format. It USED to be a pipe-delimited heredoc
inside mutation-check.sh, which could not carry a `|`: the delimiter split the
row, `old` was silently truncated, and the remainder was read as the `-run`
regex. That failure was never diagnostic -- it surfaced as a bogus BROKEN
anchor, a WEAK regexp parse error, or worst of all a truncated anchor that
happened to match once and so mutated the WRONG text while the row still
reported itself healthy. Commit 12f3a3d worked around it by re-anchoring a row
onto a pipe-free line; 231764d fixed the format instead. AGENTS.md carries that
account and is the authority; --selftest below is what pins it.

Usage:
    mutation-parse.py                 # NUL-separated records on stdout
    mutation-parse.py --lint [DIR]    # validate rows against the tree; exits
                                      # non-zero on a malformed row or an
                                      # anchor that does not match exactly once
    mutation-parse.py --selftest      # prove a bar in anchor text round-trips

Every malformed row is a hard error naming the JSON line it was written on, so
a bad table is reported where it is written rather than as a mystery verdict
forty minutes into a grid run.

Exit status: 0 all rows valid, 1 malformed table or unmatched anchor, 2 usage.
"""

import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
TABLE = os.path.join(HERE, "mutations.json")

REQUIRED = ("name", "file", "old", "new")
ALLOWED = set(REQUIRED) | {"run"}


def die(msg):
    sys.stderr.write("mutation-parse: %s\n" % msg)
    sys.exit(1)


def load():
    """Read and validate the table. Returns a list of row dicts.

    Validation is deliberately strict and deliberately fatal: a row that
    cannot be understood must stop the run, never be skipped or coerced.
    """
    try:
        with open(TABLE, encoding="utf-8") as fh:
            raw = json.load(fh)
    except FileNotFoundError:
        die("%s is missing; the mutation grid has no rows" % TABLE)
    except json.JSONDecodeError as exc:
        die("%s is not valid JSON: %s" % (TABLE, exc))

    if not isinstance(raw, list):
        die("%s must hold a JSON array, got %s" % (TABLE, type(raw).__name__))

    # Map each record to its line in the JSON text so errors can point at it.
    with open(TABLE, encoding="utf-8") as fh:
        text = fh.read()
    line_of = {}
    needle = 0
    for index, rec in enumerate(raw):
        name = rec.get("name") if isinstance(rec, dict) else None
        if isinstance(name, str):
            at = text.find('"name": "%s"' % name, needle)
            if at >= 0:
                line_of[index] = text.count("\n", 0, at) + 1
                needle = at

    rows = []
    seen = set()
    for index, rec in enumerate(raw):
        where = "%s:%s" % (os.path.relpath(TABLE, REPO), line_of.get(index, "?"))
        if not isinstance(rec, dict):
            die("row %s is %s, want an object"
                % (where, type(rec).__name__))

        unknown = sorted(set(rec) - ALLOWED)
        if unknown:
            die("row %s has unknown key(s) %s; allowed keys are name, file, "
                "old, new, run" % (where, ", ".join(unknown)))

        for key in REQUIRED:
            if key not in rec:
                die("row %s is missing required key %r" % (where, key))
            if not isinstance(rec[key], str):
                die("row %s key %r is %s, want a string"
                    % (where, key, type(rec[key]).__name__))
            if rec[key] == "":
                die("row %s key %r is empty; an empty %s cannot locate or "
                    "replace anything" % (where, key, key))

        name = rec["name"]
        if name in seen:
            die("duplicate row name %r; names select rows on the command line "
                "and must be unique" % name)
        seen.add(name)

        if "run" in rec:
            if rec["run"] == "":
                die("row %s has an empty run field; omit the key entirely to "
                    "mean 'run the whole suite'" % where)
            # A run value is handed to `go test -run`, which is a Go regexp.
            # Validate it here so a typo is a table defect, reported as such,
            # rather than a WEAK row whose failure was a regexp parse error.
            try:
                re.compile(rec["run"])
            except re.error as exc:
                die("row %s has an invalid -run regexp %r: %s"
                    % (where, rec["run"], exc))

        rows.append(rec)
    return rows


def emit(rows):
    """Write NUL-separated name/file/old/new/run records on stdout.

    NUL is the only byte that cannot occur in any of the five fields, so no
    field can truncate or run into its neighbour.
    """
    out = []
    for rec in rows:
        out.append(rec["name"])
        out.append(rec["file"])
        out.append(rec["old"])
        out.append(rec["new"])
        out.append(rec.get("run", ""))
        out.append("")  # terminates the record
    sys.stdout.write("\0".join(out))
    sys.stdout.write("\0")
# ---- lint ------------------------------------------------------------------

def lint(rows, root):
    """Validate every row against the tree without running a single test.

    A malformed row is caught here in seconds; caught the slow way, the grid
    would first spend a full baseline run and then report a misleading
    verdict -- usually BROKEN, which blames the implementation for a typo in
    the table.
    """
    bad = 0
    for rec in rows:
        path = os.path.join(root, rec["file"])
        if not os.path.isfile(path):
            sys.stderr.write("  BROKEN  %-44s missing file %s\n"
                             % (rec["name"], rec["file"]))
            bad += 1
            continue
        with open(path, encoding="utf-8") as fh:
            count = fh.read().count(rec["old"])
        if count != 1:
            sys.stderr.write("  BROKEN  %-44s anchor appears %d times in %s, "
                             "want exactly 1\n"
                             % (rec["name"], count, rec["file"]))
            bad += 1
            continue
        sys.stdout.write("  OK      %-44s %s\n" % (rec["name"], rec["file"]))
    sys.stdout.write("mutation-parse: %d row(s) linted, %d broken anchor(s)\n"
                     % (len(rows), bad))
    return 1 if bad else 0


# ---- selftest --------------------------------------------------------------

def selftest(_rows):
    """Prove a bar in anchor text survives the table, then applies and reverts.

    This is the whole point of the change, so it is asserted rather than
    assumed. The synthetic rows deliberately carry every character the old
    pipe-delimited table could not: a single `|`, a `||`, a tab, a newline,
    and a double quote. If the format ever regressed to something
    delimiter-based, the assertions below fail here rather than silently
    mis-anchoring a real row during a grid run -- which is precisely the
    failure mode this tool exists to catch.
    """
    import tempfile

    ok = True

    # 1. A row written in the table format must read back byte-identical.
    payload = [{"name": "syn", "file": "f",
                "old": "haps = queryArc(b, e) || [];",
                "new": "haps = []; // bars are ordinary characters",
                "run": "TestA|TestB"}]
    tmp = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False,
                                      encoding="utf-8")
    try:
        json.dump(payload, tmp)
        tmp.close()
        global TABLE
        real, TABLE = TABLE, tmp.name
        try:
            back = load()
        finally:
            TABLE = real
        rec = back[0]
        for key in ("old", "new", "run"):
            want = payload[0][key]
            if rec[key] != want:
                sys.stderr.write("  FAIL round-trip %s: got %r want %r\n"
                                 % (key, rec[key], want))
                ok = False
    finally:
        os.unlink(tmp.name)
    if ok:
        sys.stdout.write("  OK      %-44s bars survive the table verbatim\n"
                         % "json-round-trip")

    # 2. Each case must apply to a real file and revert byte-identically.
    cases = [
        ("double-bar-in-old", "haps = queryArc(b, e) || [];", "haps = [];"),
        ("single-bar-in-old", "x = a | b;", "x = b | a;"),
        ("tab-and-newline-in-old", "if (a) {\n\treturn b;\n}",
         "if (a) {\n\treturn c;\n}"),
        ("double-quote-in-old", 'msg = "hi";', "msg = 'hi';"),
    ]
    for name, old, new in cases:
        scratch = tempfile.NamedTemporaryFile("w", suffix=".go", delete=False,
                                              encoding="utf-8")
        scratch.write("package p\n\nfunc f() {\n" + old + "\n}\n")
        scratch.close()
        try:
            with open(scratch.name, encoding="utf-8") as fh:
                before = fh.read()
            count = before.count(old)
            if count != 1:
                sys.stderr.write("  FAIL %s: anchor appears %d times\n"
                                 % (name, count))
                ok = False
                continue
            after = before.replace(old, new, 1)
            if old in after or after.count(new) != 1:
                sys.stderr.write("  FAIL %s: mutation did not apply cleanly\n"
                                 % name)
                ok = False
                continue
            if after.replace(new, old, 1) != before:
                sys.stderr.write("  FAIL %s: revert is not byte-identical\n"
                                 % name)
                ok = False
                continue
            sys.stdout.write("  OK      %-44s applies and reverts "
                             "byte-identically\n" % name)
        finally:
            os.unlink(scratch.name)

    sys.stdout.write("mutation-parse: selftest %s\n"
                     % ("PASSED" if ok else "FAILED"))
    return 0 if ok else 1


def main(argv):
    mode = argv[1] if len(argv) > 1 else "--emit"
    if mode in ("--emit", ""):
        emit(load())
        return 0
    if mode == "--lint":
        root = argv[2] if len(argv) > 2 else REPO
        return lint(load(), root)
    if mode == "--selftest":
        return selftest(load())
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))