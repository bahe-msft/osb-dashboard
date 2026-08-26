#!/usr/bin/env python3
"""Generate visual and structural inspection artifacts from the Swapbook spec."""

from __future__ import annotations

import argparse
import datetime as dt
import html
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.parse
import urllib.request


def run(command: list[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(command, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if check and result.returncode != 0:
        raise RuntimeError(f"command failed ({result.returncode}): {' '.join(command)}\n{result.stdout}")
    return result


def pw(session: str, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return run(["playwright-cli", f"-s={session}", *args], check=check)


def frame_url(gallery: str, case: dict[str, object]) -> str:
    query: dict[str, str] = {
        "mode": "mock",
        "bg": "light",
        "htmx": "/assets/third-party/ui/htmx.min.js",
        "css": "/assets/styles.css",
        "js": "/assets/app.js",
    }
    for key, value in (case.get("args") or {}).items():
        query[f"arg.{key}"] = str(value)
    story = urllib.parse.quote(str(case["story"]), safe="")
    variant = urllib.parse.quote(str(case["variant"]), safe="")
    return f"{gallery.rstrip('/')}/__sb/frame/{story}/{variant}?{urllib.parse.urlencode(query)}"


def assertion_script(case: dict[str, object], output: Path) -> Path:
    assertions = json.dumps(case.get("assertions") or [])
    script = f"""async page => {{
  await page.waitForLoadState('domcontentloaded');
  await page.waitForFunction(() => !document.querySelector('[aria-label="Loading dashboard content"]'), null, {{ timeout: 15000 }});
  await page.waitForTimeout(250);
  const assertions = {assertions};
  const results = [];
  for (const assertion of assertions) {{
    const locator = page.locator(assertion.selector);
    const count = await locator.count();
    let actual;
    let passed = false;
    if (assertion.type === 'visible') {{
      actual = count > 0 && await locator.first().isVisible();
      passed = actual === true;
    }} else if (assertion.type === 'text') {{
      actual = count ? (await locator.allTextContents()).join(' ').replace(/\\s+/g, ' ').trim() : '';
      passed = actual.includes(String(assertion.expected));
    }} else if (assertion.type === 'count') {{
      actual = count;
      passed = actual === Number(assertion.expected);
    }} else if (assertion.type === 'attribute') {{
      actual = count ? await locator.first().getAttribute(assertion.attribute) : null;
      passed = count > 0 && actual === String(assertion.expected);
    }} else if (assertion.type === 'no-overflow') {{
      actual = count ? await locator.first().evaluate(el => ({{ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }})) : null;
      passed = actual !== null && actual.scrollWidth <= actual.clientWidth;
    }} else {{
      actual = 'unknown assertion type';
    }}
    const result = {{ ...assertion, actual, passed }};
    results.push(result);
    if (!passed) throw new Error(`inspection assertion failed: ${{JSON.stringify(result)}}`);
  }}
  const pageOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  if (pageOverflow) throw new Error('inspection assertion failed: page has horizontal overflow');
  return {{ title: await page.title(), url: page.url(), pageOverflow, assertions: results }};
}}
"""
    path = output / "assert.js"
    path.write_text(script)
    return path


def write_report(output: Path, report: dict[str, object]) -> None:
    (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    cards = []
    for result in report["results"]:
        status = result["status"]
        assertions = html.escape(result.get("assertionOutput", ""))
        sources = "".join(f"<li><code>{html.escape(source)}</code></li>" for source in result.get("sources", []))
        cards.append(f"""
<section class="case {status}">
  <h2>{html.escape(result['id'])} <small>{html.escape(result['viewport'])}</small></h2>
  <p><strong>{status.upper()}</strong> · <code>{html.escape(result['frameURL'])}</code></p>
  <img src="{html.escape(result['screenshot'])}" alt="{html.escape(result['id'])} at {html.escape(result['viewport'])}">
  <details><summary>Code assertions</summary><pre>{assertions}</pre></details>
  <details><summary>Semantic snapshot</summary><p><a href="{html.escape(result['semantic'])}">Open ARIA snapshot</a></p></details>
  <details><summary>Rendered HTML</summary><p><a href="{html.escape(result['html'])}">Open HTML</a></p></details>
  <details><summary>Relevant source</summary><ul>{sources}</ul></details>
</section>""")
    document = f"""<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="icon" href="data:,">
<title>OpenSandbox Swapbook inspection</title>
<style>
body{{margin:0;padding:24px;background:#f3f4f6;color:#18181b;font:14px/1.5 system-ui,sans-serif}}main{{max-width:1500px;margin:auto}}h1{{margin-top:0}}.summary{{padding:14px 16px;border:1px solid #d4d4d8;border-radius:10px;background:white}}.case{{margin:20px 0;padding:16px;border:1px solid #d4d4d8;border-left:5px solid #22c55e;border-radius:10px;background:white}}.case.failed{{border-left-color:#ef4444}}h2{{margin:0 0 6px}}h2 small{{color:#71717a;font-weight:500}}img{{display:block;max-width:100%;margin:14px 0;border:1px solid #d4d4d8;border-radius:8px}}pre{{overflow:auto;padding:12px;background:#18181b;color:#e4e4e7;border-radius:7px}}code{{overflow-wrap:anywhere}}details{{margin-top:8px}}summary{{cursor:pointer}}
</style></head><body><main>
<h1>OpenSandbox Swapbook inspection</h1>
<div class="summary"><strong>{report['passed']} passed · {report['failed']} failed</strong><br>Generated {html.escape(report['generatedAt'])}</div>
{''.join(cards)}
</main></body></html>"""
    (output / "report.html").write_text(document)
    (output / "llm-review.md").write_text(
        "# OpenSandbox UI inspection\n\n"
        "Review `report.json` together with each PNG and `.aria.yml` artifact. "
        "Check visual hierarchy, spacing, clipping, cursor/disabled affordances, semantic labels, "
        "state consistency, and any failed code assertion. Cite the case ID and viewport for every finding.\n"
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--gallery", default="http://127.0.0.1:7007")
    parser.add_argument("--target", default="http://127.0.0.1:8081")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()

    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    with urllib.request.urlopen(f"{args.target.rstrip('/')}/_swapbook/inspection.json") as response:
        spec = json.load(response)
    (output / "spec.json").write_text(json.dumps(spec, indent=2) + "\n")

    viewports = {viewport["name"]: viewport for viewport in spec["viewports"]}
    results: list[dict[str, object]] = []
    failures = 0
    index = 0
    for case in spec["cases"]:
        for viewport_name in case["viewports"]:
            index += 1
            viewport = viewports[viewport_name]
            artifact_id = re.sub(r"[^a-z0-9-]+", "-", f"{case['id']}-{viewport_name}".lower()).strip("-")
            case_dir = output / artifact_id
            case_dir.mkdir(parents=True, exist_ok=True)
            session = f"sb-inspect-{os.getpid()}-{index}"
            url = frame_url(args.gallery, case)
            result: dict[str, object] = {
                "id": case["id"], "viewport": viewport_name, "frameURL": url,
                "sources": case.get("sources", []), "status": "passed",
                "screenshot": f"{artifact_id}/visual.png", "semantic": f"{artifact_id}/semantic.aria.yml",
                "html": f"{artifact_id}/rendered.html",
            }
            try:
                pw(session, "open", url, "--browser", "chromium")
                pw(session, "resize", str(viewport["width"]), str(viewport["height"]))
                script = assertion_script(case, case_dir)
                assertion = pw(session, "--raw", "run-code", "--filename", str(script), check=False)
                result["assertionOutput"] = assertion.stdout.strip()
                if assertion.returncode != 0 or "### Error" in assertion.stdout:
                    raise RuntimeError(assertion.stdout.strip() or "assertion runner failed")
                pw(session, "screenshot", "--filename", str(case_dir / "visual.png"))
                semantic = pw(session, "snapshot")
                (case_dir / "semantic.aria.yml").write_text(semantic.stdout)
                rendered = pw(session, "--raw", "eval", "() => document.documentElement.outerHTML", check=False)
                rendered_text = rendered.stdout.strip()
                try:
                    rendered_text = json.loads(rendered_text)
                except (json.JSONDecodeError, TypeError):
                    pass
                (case_dir / "rendered.html").write_text(str(rendered_text))
                console = pw(session, "console", "warning")
                (case_dir / "console.log").write_text(console.stdout)
                if not re.search(r"Errors:\s*0", console.stdout):
                    raise RuntimeError("browser console contains errors; see console.log")
                network = pw(session, "requests")
                (case_dir / "network.log").write_text(network.stdout)
            except Exception as exc:  # noqa: BLE001 - report every case before failing the run
                failures += 1
                result["status"] = "failed"
                result["error"] = str(exc)
            finally:
                pw(session, "close", check=False)
            results.append(result)
            print(f"{result['status']:>6}  {case['id']} · {viewport_name}")

    report = {
        "version": spec["version"],
        "generatedAt": dt.datetime.now(dt.timezone.utc).isoformat(),
        "passed": len(results) - failures,
        "failed": failures,
        "results": results,
    }
    write_report(output, report)
    print(f"\nInspection report: {output / 'report.html'}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
