"""Optional browser regression: python3 -m unittest discover -s web.

Requires Playwright and a Chromium browser. Uses intercepted fixtures only.
"""
import json
import os
from pathlib import Path
import shutil
import time
import unittest

try:
    from playwright.sync_api import sync_playwright
except ImportError:
    sync_playwright = None


@unittest.skipUnless(sync_playwright, "Playwright is not installed")
class DashboardTest(unittest.TestCase):
    def test_kind_filter_and_detail(self):
        html = Path(__file__).with_name("index.html").read_text()
        agents = [
            dict(session_id="codex-session", agent_kind="codex", role="implementer", task="kind-demo", state="active", host="workstation", cwd="/work/agistry", last_seen=int(time.time())),
            dict(session_id="claude-session", agent_kind="claude", role="reviewer", task="kind-demo", state="active", host="workstation", cwd="/work/agistry", last_seen=int(time.time())),
            dict(session_id="legacy-session", role="researcher", task="kind-demo", state="unassigned", host="workstation", cwd="/work/agistry", last_seen=int(time.time())),
        ]
        errors = []
        with sync_playwright() as pw:
            executable = os.environ.get("AGISTRY_TEST_BROWSER") or shutil.which("google-chrome") or shutil.which("chromium")
            browser = pw.chromium.launch(**({"executable_path": executable} if executable else {}), args=["--no-sandbox"], timeout=15000)
            page = browser.new_page(viewport={"width": 1280, "height": 800}, device_scale_factor=1)
            page.on("pageerror", lambda e: errors.append(str(e)))

            def fixture(route):
                path = route.request.url.split("agistry.test", 1)[-1].split("?", 1)[0]
                if path == "/":
                    route.fulfill(content_type="text/html", body=html)
                else:
                    payload = {"/agents": {"agents": agents}, "/messages": {"messages": []}, "/resources": {"resources": []}, "/conversations": {"conversations": []}}.get(path, {})
                    route.fulfill(content_type="application/json", body=json.dumps(payload))

            page.route("http://agistry.test/**", fixture)
            # Capture labels to click actual canvas nodes after layout, without
            # exposing dashboard internals in production code.
            page.add_init_script("""window.agentLabels = {};
                const fillText = CanvasRenderingContext2D.prototype.fillText;
                CanvasRenderingContext2D.prototype.fillText = function(text,x,y){
                    if (['implementer','reviewer','researcher'].includes(text)) window.agentLabels[text]={x,y};
                    return fillText.apply(this,arguments);
                };""")
            page.goto("http://agistry.test/")
            page.wait_for_function("document.getElementById('summary').textContent.startsWith('3 agents')")
            page.select_option("#every", "0")
            page.click('button[data-view="table"]')
            self.assertEqual(page.locator("tr.member").count(), 3)
            self.assertEqual(page.locator("td.agent-kind").all_text_contents(), ["Codex", "Claude Code", "Unknown"])
            for kind, label in [("codex", "Codex"), ("claude", "Claude Code"), ("unknown", "Unknown")]:
                page.select_option("#agentKind", kind)
                self.assertEqual(page.locator("tr.member").count(), 1)
                self.assertEqual(page.locator("td.agent-kind").inner_text(), label)
                self.assertTrue(page.locator("#summary").inner_text().startswith("1 agents"))
            page.reload()
            page.wait_for_selector("tr.member")
            self.assertEqual(page.locator("#agentKind").input_value(), "unknown")
            self.assertEqual(page.locator("td.agent-kind").inner_text(), "Unknown")
            page.select_option("#agentKind", "")
            page.click('button[data-view="graph"]')
            page.wait_for_function("window.agentLabels.implementer")
            page.wait_for_timeout(1200)
            if os.environ.get("AGISTRY_TEST_SCREENSHOT"):
                page.screenshot(path=os.environ["AGISTRY_TEST_SCREENSHOT"])
            point = page.evaluate("window.agentLabels.implementer")
            canvas = page.locator("#graphCanvas").bounding_box()
            page.mouse.click(canvas["x"] + point["x"], canvas["y"] + point["y"] - 16)
            page.wait_for_selector("#npGrid")
            self.assertIn("Codex", page.locator("#npGrid").inner_text())
            page.select_option("#agentKind", "claude")
            self.assertTrue(page.locator("#summary").inner_text().startswith("1 agents"))
            self.assertEqual(errors, [])
            browser.close()


if __name__ == "__main__":
    unittest.main()
