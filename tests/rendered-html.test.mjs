import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// The built worker is exercised directly so these checks cover the deployed
// server-rendered output without depending on a browser or development server.
async function render(pathname = "/") {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}-${pathname}`);
  const { default: worker } = await import(workerUrl.href);

  return worker.fetch(
    new Request(`http://localhost${pathname}`, {
      headers: { accept: "text/html" },
    }),
    {
      ASSETS: {
        fetch: async () => new Response("Not found", { status: 404 }),
      },
    },
    {
      waitUntil() {},
      passThroughOnException() {},
    },
  );
}

test("server-renders the RepoMender dashboard", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);

  const html = await response.text();
  assert.match(html, /<title>RepoMender · Review\. Diagnose\. Repair\.<\/title>/i);
  assert.match(html, /RepoMender/);
  assert.match(html, /What needs your attention/);
  assert.match(html, /Code reviews/);
  assert.match(html, /CI diagnostics/);
  assert.match(html, /Issue repairs/);
  assert.match(html, /Approval center|Approvals/);
  assert.doesNotMatch(html, /Your site is taking shape|react-loading-skeleton/);
});

test("direct product routes preserve the application shell", async () => {
  for (const pathname of ["/reviews", "/diagnostics/pg16", "/repairs/731", "/settings", "/login"]) {
    const response = await render(pathname);
    assert.equal(response.status, 200, pathname);
    const html = await response.text();
    assert.match(html, /RepoMender/, pathname);
    if (pathname === "/login") {
      assert.match(html, /Continue with enterprise SSO/, pathname);
      assert.match(html, /emergency local administrator/, pathname);
    } else {
      assert.match(html, /Primary navigation/, pathname);
    }
  }
});

test("source keeps navigation, approval, and accessibility behaviors wired", async () => {
  const [app, packageJson] = await Promise.all([
    readFile(new URL("../app/components/RepoMenderApp.tsx", import.meta.url), "utf8"),
    readFile(new URL("../package.json", import.meta.url), "utf8"),
  ]);

  assert.match(app, /router\.push\(route\)/);
  assert.match(app, /aria-label="Primary navigation"/);
  assert.match(app, /aria-modal="true"/);
  assert.match(app, /event\.key === "Enter"/);
  assert.match(app, /localStorage\.setItem\("repomender-theme"/);
  assert.match(app, /\/api\/v1\/auth\/\$\{mode\}/);
  assert.match(app, /Continue with enterprise SSO/);
  assert.doesNotMatch(packageJson, /react-loading-skeleton/);
});
