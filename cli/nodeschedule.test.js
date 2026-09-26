const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const { parseScript, parseEnv, buildResources } = require("./resources");

test("preserves a runnable ES module and references all env values through a Secret", () => {
  const script = `export const schedule = "0 7 * * *";\nexport const timeZone = "America/New_York";\nconsole.log(process.env.TOKEN);\n`;
  const parsed = parseScript(script);
  const env = parseEnv("TOKEN='a secret'\nDISCORD_WEBHOOK_URL=https://example.test/hook#fragment\n");
  assert.equal(parsed.schedule, "0 7 * * *");
  assert.equal(parsed.timeZone, "America/New_York");
  assert.equal(parsed.source, script);
  const [secret, configMap, resource] = buildResources({ name: "daily", namespace: "automation", ...parsed, env });
  assert.equal(secret.data.TOKEN, Buffer.from("a secret").toString("base64"));
  assert.equal(configMap.data["script.js"], parsed.source);
  assert.equal(resource.spec.env.length, 2);
  assert.equal(resource.spec.env[1].valueFrom.secretKeyRef.name, "daily-secrets");
  assert.equal(resource.spec.timeZone, "America/New_York");
  assert.doesNotMatch(JSON.stringify(resource), /a secret|example\.test/);
});

test("rejects missing or dynamic schedule and duplicate env keys", () => {
  assert.throws(() => parseScript("const schedule = '0 7 * * *';"), /literal export/);
  assert.throws(() => parseScript("export const schedule = process.env.CRON;"), /literal export/);
  assert.throws(() => parseEnv("TOKEN=one\nTOKEN=two"), /Duplicate/);
});

test("deploy invokes kubectl for Secret, ConfigMap, and NodeSchedule in order", () => {
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "nodeschedule-cli-"));
  try {
    const folder = path.join(temp, "daily");
    fs.mkdirSync(folder);
    fs.writeFileSync(path.join(folder, "script.js"), 'export const schedule = "@daily";\nconsole.log(process.env.TOKEN);\n');
    fs.writeFileSync(path.join(folder, ".env"), "TOKEN=secret-value\n");
    const stub = path.join(temp, "kubectl");
    fs.writeFileSync(stub, `#!/usr/bin/env node
const fs = require('node:fs');
fs.appendFileSync(process.env.CAPTURE, JSON.stringify({args: process.argv.slice(2), resource: JSON.parse(fs.readFileSync(0, 'utf8'))}) + '\\n');
console.log('applied');
`, { mode: 0o755 });
    const capture = path.join(temp, "capture");
    const result = spawnSync(process.execPath, [path.join(__dirname, "nodeschedule.js"), "deploy", folder, "--context", "test-cluster"], {
      encoding: "utf8", env: { ...process.env, PATH: `${temp}:${process.env.PATH}`, CAPTURE: capture },
    });
    assert.equal(result.status, 0, result.stderr);
    const calls = fs.readFileSync(capture, "utf8").trim().split("\n").map(JSON.parse);
    assert.deepEqual(calls.map((call) => call.resource.kind), ["Secret", "ConfigMap", "NodeSchedule"]);
    assert.ok(calls.every((call) => call.args.includes("--server-side") && call.args.includes("test-cluster")));
    assert.equal(calls[0].resource.data.TOKEN, Buffer.from("secret-value").toString("base64"));
    assert.equal(calls[2].resource.spec.timeZone, "Etc/UTC");
  } finally {
    fs.rmSync(temp, { recursive: true, force: true });
  }
});
