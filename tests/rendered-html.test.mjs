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
  assert.match(html, /需要你关注/);
  assert.match(html, /代码审查/);
  assert.match(html, /CI 诊断/);
  assert.match(html, /Issue 修复/);
  assert.match(html, /审批/);
  assert.doesNotMatch(html, /Your site is taking shape|react-loading-skeleton/);
});

test("direct product routes preserve the application shell", async () => {
  for (const pathname of ["/reviews", "/diagnostics/pg16", "/repairs/731", "/repositories", "/repositories/test-id", "/runs/diagnostic", "/settings", "/login"]) {
    const response = await render(pathname);
    assert.equal(response.status, 200, pathname);
    const html = await response.text();
    assert.match(html, /RepoMender/, pathname);
    if (pathname === "/login") {
      assert.match(html, /继续使用企业单点登录/, pathname);
      assert.match(html, /紧急本地管理员账号/, pathname);
    } else {
      assert.match(html, /主导航|Primary navigation/, pathname);
    }
  }
});

test("source keeps navigation, approval, and accessibility behaviors wired", async () => {
  const [app, i18n, packageJson] = await Promise.all([
    readFile(new URL("../app/components/RepoMenderApp.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/lib/i18n.tsx", import.meta.url), "utf8"),
    readFile(new URL("../package.json", import.meta.url), "utf8"),
  ]);

  assert.match(app, /router\.push\(route\)/);
  assert.match(app, /primaryNavigation/);
  assert.match(app, /aria-modal="true"/);
  assert.match(app, /event\.key === "Enter"/);
  assert.match(app, /localStorage\.setItem\("repomender-theme"/);
  assert.match(i18n, /repomender-language/);
  assert.match(app, /LanguageProvider/);
  assert.match(app, /setLanguage\(language === "zh" \? "en" : "zh"\)/);
  assert.match(app, /\/api\/v1\/auth\/\$\{mode\}/);
  assert.match(app, /continueSso/);
  assert.match(app, /\/api\/v1\/scm\/connections/);
  assert.match(app, /\/api\/v1\/repositories/);
  assert.match(app, /\/api\/v1\/approvals/);
  assert.match(app, /Approval state was not replaced with browser mocks/);
  assert.match(app, /installGithubApp/);
  assert.match(app, /connectGitlab/);
  assert.match(app, /providers\.gitlab \?/);
  assert.match(app, /\/api\/v1\/internal\/executions/);
  assert.match(app, /Agent Compose execution diagnostic/);
  assert.doesNotMatch(packageJson, /react-loading-skeleton/);
});
