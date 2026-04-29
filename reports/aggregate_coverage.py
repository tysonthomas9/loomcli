#!/usr/bin/env python3
"""Aggregate per-contract validation + E2E audit reports into a coverage matrix.

Reads the 21 ``reports/validation-*.json`` and 21 ``reports/e2e-audit-*.json``
files, enforces invariants, and writes ``reports/coverage-matrix.json`` and
``reports/coverage-matrix.md``. Run from the repo root.
"""
import datetime as _dt
import glob
import json
import os
import re
import sys

REPORTS_DIR = "reports"
VALIDATION_GLOB = os.path.join(REPORTS_DIR, "validation-*.json")
AUDIT_PATH_FMT = os.path.join(REPORTS_DIR, "e2e-audit-{short}.json")
JSON_OUT = os.path.join(REPORTS_DIR, "coverage-matrix.json")
MD_OUT = os.path.join(REPORTS_DIR, "coverage-matrix.md")

VALID_STATUSES = {"pass", "fail", "blocked", "deferred"}
VALID_COVERAGES = {"covered", "partial", "uncovered"}

VALIDATION_RE = re.compile(r"^validation-(?P<short>[A-Za-z0-9_-]+)\.json$")


def _die(msg):
    sys.stderr.write(msg.rstrip() + "\n")
    sys.exit(1)


def _short_from_validation_path(path):
    base = os.path.basename(path)
    m = VALIDATION_RE.match(base)
    if not m:
        _die(f"unexpected validation filename: {path}")
    return m.group("short")


def _load_json(path):
    try:
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        _die(f"failed to load {path}: {e}")


def _check_validation(path, short, data):
    """Validate one validation-*.json report and return per-contract numbers."""
    contract_file = data.get("contract_file")
    if not contract_file:
        _die(f"{path}: missing contract_file")
    assertion_count = data.get("assertion_count")
    if not isinstance(assertion_count, int):
        _die(f"{path}: missing/invalid assertion_count")
    results = data.get("results")
    if not isinstance(results, dict):
        _die(f"{path}: missing/invalid results object")
    for key in ("pass", "fail", "blocked", "deferred"):
        if key not in results or not isinstance(results[key], int):
            _die(f"{path}: missing/invalid results.{key}")

    assertions = data.get("assertions")
    if not isinstance(assertions, list):
        _die(f"{path}: missing/invalid assertions array")
    for a in assertions:
        st = a.get("status")
        if st not in VALID_STATUSES:
            _die(
                f"{path}: assertion {a.get('id', '?')!r} has unexpected status "
                f"{st!r}; allowed: {sorted(VALID_STATUSES)}"
            )

    p, f_, b, df = results["pass"], results["fail"], results["blocked"], results["deferred"]
    if p + f_ + b + df != assertion_count:
        _die(
            f"{path}: invariant violation: pass+fail+blocked+deferred "
            f"({p}+{f_}+{b}+{df}={p+f_+b+df}) != assertion_count ({assertion_count})"
        )
    if len(assertions) != assertion_count:
        _die(
            f"{path}: invariant violation: len(assertions)={len(assertions)} "
            f"!= assertion_count={assertion_count}"
        )
    return {
        "contract_file": contract_file,
        "total": assertion_count,
        "pass": p,
        "fail": f_,
        "blocked": b,
        "deferred": df,
    }


def _check_audit(path, short, data, paired_pass):
    """Validate one e2e-audit-*.json report and return per-contract numbers."""
    contract_file = data.get("contract_file")
    if not contract_file:
        _die(f"{path}: missing contract_file")
    summary = data.get("summary")
    if not isinstance(summary, dict):
        _die(f"{path}: missing/invalid summary object")
    for key in ("total_passing", "covered", "partial", "uncovered"):
        if key not in summary or not isinstance(summary[key], int):
            _die(f"{path}: missing/invalid summary.{key}")
    stored_pct = summary.get("coverage_pct")
    if stored_pct is not None and not isinstance(stored_pct, (int, float)):
        _die(
            f"{path}: summary.coverage_pct is not numeric: {stored_pct!r}"
        )

    assertions = data.get("assertions")
    if not isinstance(assertions, list):
        _die(f"{path}: missing/invalid assertions array")
    for a in assertions:
        cov = a.get("coverage")
        if cov not in VALID_COVERAGES:
            _die(
                f"{path}: assertion {a.get('id', '?')!r} has unexpected coverage "
                f"{cov!r}; allowed: {sorted(VALID_COVERAGES)}"
            )

    tp = summary["total_passing"]
    cov_n = summary["covered"]
    par_n = summary["partial"]
    unc_n = summary["uncovered"]
    if cov_n + par_n + unc_n != tp:
        _die(
            f"{path}: invariant violation: covered+partial+uncovered "
            f"({cov_n}+{par_n}+{unc_n}={cov_n+par_n+unc_n}) != total_passing ({tp})"
        )
    if len(assertions) != tp:
        _die(
            f"{path}: invariant violation: len(assertions)={len(assertions)} "
            f"!= total_passing={tp}"
        )
    if tp != paired_pass:
        _die(
            f"{path}: invariant violation: total_passing ({tp}) != paired "
            f"validation pass ({paired_pass})"
        )

    recomputed = (cov_n / tp * 100.0) if tp > 0 else None
    if stored_pct is not None and recomputed is not None:
        if abs(recomputed - float(stored_pct)) > 0.1:
            _die(
                f"{path}: invariant violation: stored coverage_pct ({stored_pct}) "
                f"differs from recomputed ({recomputed:.4f}) by more than 0.1"
            )

    return {
        "contract_file": contract_file,
        "covered": cov_n,
        "partial": par_n,
        "uncovered": unc_n,
        "total_passing": tp,
    }


def _coverage_pct(covered, pass_count):
    if pass_count == 0:
        return None
    return round(covered / pass_count * 100.0, 1)


def _md_pct(pct):
    return "—" if pct is None else f"{pct:.1f}%"


def main():
    validation_paths = sorted(glob.glob(VALIDATION_GLOB))
    if not validation_paths:
        _die(f"no validation reports found at {VALIDATION_GLOB}")

    seen_shorts = {}
    rows = []
    for vpath in validation_paths:
        short = _short_from_validation_path(vpath)
        if short in seen_shorts:
            _die(
                f"duplicate contract short name {short!r}: "
                f"{seen_shorts[short]} and {vpath}"
            )
        seen_shorts[short] = vpath

        vdata = _load_json(vpath)
        vinfo = _check_validation(vpath, short, vdata)

        apath = AUDIT_PATH_FMT.format(short=short)
        if not os.path.exists(apath):
            _die(
                f"missing audit report for short_name {short!r}: expected {apath} "
                f"(paired with {vpath})"
            )
        adata = _load_json(apath)
        ainfo = _check_audit(apath, short, adata, paired_pass=vinfo["pass"])

        if vinfo["contract_file"] != ainfo["contract_file"]:
            _die(
                f"contract_file mismatch for {short!r}: validation says "
                f"{vinfo['contract_file']!r}, audit says {ainfo['contract_file']!r}"
            )

        pct = _coverage_pct(ainfo["covered"], vinfo["pass"])
        rows.append({
            "contract_file": vinfo["contract_file"],
            "short_name": short,
            "validation_report": vpath,
            "audit_report": apath,
            "total": vinfo["total"],
            "pass": vinfo["pass"],
            "fail": vinfo["fail"],
            "blocked": vinfo["blocked"],
            "deferred": vinfo["deferred"],
            "e2e_covered": ainfo["covered"],
            "e2e_partial": ainfo["partial"],
            "e2e_uncovered": ainfo["uncovered"],
            "coverage_pct": pct,
        })

    audit_paths = sorted(glob.glob(os.path.join(REPORTS_DIR, "e2e-audit-*.json")))
    expected_audits = {AUDIT_PATH_FMT.format(short=s) for s in seen_shorts}
    orphans = sorted(set(audit_paths) - expected_audits)
    if orphans:
        _die(
            "extra audit report(s) with no matching validation report: "
            + ", ".join(orphans)
        )

    rows.sort(key=lambda r: r["short_name"])

    totals = {
        "total": sum(r["total"] for r in rows),
        "pass": sum(r["pass"] for r in rows),
        "fail": sum(r["fail"] for r in rows),
        "blocked": sum(r["blocked"] for r in rows),
        "deferred": sum(r["deferred"] for r in rows),
        "e2e_covered": sum(r["e2e_covered"] for r in rows),
        "e2e_partial": sum(r["e2e_partial"] for r in rows),
        "e2e_uncovered": sum(r["e2e_uncovered"] for r in rows),
    }
    totals["coverage_pct"] = _coverage_pct(totals["e2e_covered"], totals["pass"])

    if totals["total"] != 1741:
        sys.stderr.write(
            f"warning: grand-total assertion_count is {totals['total']}, expected 1741 "
            f"(planning estimate); proceeding with observed total.\n"
        )

    generated_at = _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    matrix = {
        "generated_at": generated_at,
        "validation_report_count": len(validation_paths),
        "audit_report_count": len(audit_paths),
        "contracts": rows,
        "totals": totals,
    }

    with open(JSON_OUT, "w", encoding="utf-8") as f:
        json.dump(matrix, f, indent=2, sort_keys=False)
        f.write("\n")

    _write_markdown(matrix)


def _write_markdown(matrix):
    rows = matrix["contracts"]
    totals = matrix["totals"]
    lines = []
    lines.append("# Behavioral Contract Validation — Coverage Matrix")
    lines.append("")
    lines.append(
        f"_Generated: {matrix['generated_at']} — {len(rows)} contracts, "
        f"{totals['total']} assertions._"
    )
    lines.append("")
    lines.append("**Column meanings:**")
    lines.append(
        "- **Skip** = validation `blocked` (assertion could not be executed because a "
        "prerequisite Layer failed or the environment lacked a dependency)"
    )
    lines.append(
        "- **Gap** = E2E audit `partial` (a Playwright/Go test exists but does not "
        "fully verify the assertion's evidence)"
    )
    lines.append(
        "- **E2E Covered** / **E2E Uncovered** = strict covered / uncovered from the "
        "E2E audit (excludes Gap)"
    )
    lines.append("- **Coverage %** = `E2E Covered / Pass × 100`")
    lines.append("")
    lines.append(
        "| Contract | Total | Pass | Fail | Skip | Deferred | Gap | E2E Covered "
        "| E2E Uncovered | Coverage % |"
    )
    lines.append(
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"
    )
    for r in rows:
        lines.append(
            f"| {r['contract_file']} | {r['total']} | {r['pass']} | {r['fail']} "
            f"| {r['blocked']} | {r['deferred']} | {r['e2e_partial']} "
            f"| {r['e2e_covered']} | {r['e2e_uncovered']} | {_md_pct(r['coverage_pct'])} |"
        )
    lines.append(
        f"| **TOTAL** | **{totals['total']}** | **{totals['pass']}** "
        f"| **{totals['fail']}** | **{totals['blocked']}** | **{totals['deferred']}** "
        f"| **{totals['e2e_partial']}** | **{totals['e2e_covered']}** "
        f"| **{totals['e2e_uncovered']}** | **{_md_pct(totals['coverage_pct'])}** |"
    )
    lines.append("")
    with open(MD_OUT, "w", encoding="utf-8") as f:
        f.write("\n".join(lines))


if __name__ == "__main__":
    main()
