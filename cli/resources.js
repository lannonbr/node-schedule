const namePattern = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
const envNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

function validName(value) {
  return namePattern.test(value);
}

function extractStringExport(source, name, required) {
  const declaration = new RegExp(`^([\\t ]*)export[\\t ]+const[\\t ]+${name}[\\t ]*=[\\t ]*(["'])([^\\r\\n]*?)\\2[\\t ]*;?[\\t ]*(?://[^\\r\\n]*)?$`, "gm");
  const matches = [...source.matchAll(declaration)];
  if (matches.length !== 1) {
    if (!required && matches.length === 0 && !new RegExp(`\\bexport\\s+const\\s+${name}\\b`).test(source)) return undefined;
    throw new Error(`script.js must have exactly one literal export: export const ${name} = "..."`);
  }
  const value = matches[0][3];
  if (!value || value.includes("\\")) throw new Error(`${name} must be a non-empty string literal without escapes`);
  return value;
}

function parseScript(script) {
  const schedule = extractStringExport(script, "schedule", true);
  const zone = extractStringExport(script, "timeZone", false);
  // Kubernetes CronJobs accept five-field expressions and standard @daily-style descriptors.
  const fields = schedule.trim().split(/\s+/);
  if (!(fields.length === 5 || /^@(yearly|annually|monthly|weekly|daily|midnight|hourly)$/.test(schedule))) {
    throw new Error(`Invalid cron schedule ${JSON.stringify(schedule)}; use five fields or a standard @ descriptor`);
  }
  const timeZone = zone || "Etc/UTC";
  try { new Intl.DateTimeFormat("en", { timeZone }); }
  catch { throw new Error(`Invalid time zone ${JSON.stringify(timeZone)}`); }
  return { source: script, schedule, timeZone };
}

function parseEnv(contents) {
  const values = {};
  const lines = contents.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (!line || line.startsWith("#")) continue;
    const match = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
    if (!match || !envNamePattern.test(match[1])) throw new Error(`Invalid .env entry on line ${i + 1}`);
    const key = match[1];
    if (Object.hasOwn(values, key)) throw new Error(`Duplicate .env key ${key} on line ${i + 1}`);
    let value = match[2];
    if (value.startsWith('"') || value.startsWith("'")) {
      const quote = value[0];
      const end = value.lastIndexOf(quote);
      if (end === 0 || !/^\s*(?:#.*)?$/.test(value.slice(end + 1))) throw new Error(`Invalid quoted .env value on line ${i + 1}`);
      value = value.slice(1, end);
      if (quote === '"') value = value.replace(/\\([nrt\\"])/g, (_, char) => ({ n: "\n", r: "\r", t: "\t", "\\": "\\", '"': '"' })[char]);
    } else {
      value = value.replace(/\s+#.*$/, "").trimEnd();
    }
    values[key] = value;
  }
  return values;
}

function buildResources({ name, namespace, source, schedule, timeZone, env }) {
  const scriptName = `${name}-script`;
  const secretName = `${name}-secrets`;
  if (!validName(scriptName) || !validName(secretName)) throw new Error("Resource name is too long for generated ConfigMap and Secret names");
  const metadata = (resourceName) => ({ name: resourceName, namespace });
  const secret = { apiVersion: "v1", kind: "Secret", metadata: metadata(secretName), type: "Opaque", data: Object.fromEntries(Object.entries(env).map(([key, value]) => [key, Buffer.from(value).toString("base64")])) };
  const configMap = { apiVersion: "v1", kind: "ConfigMap", metadata: metadata(scriptName), data: { "script.js": source } };
  const nodeSchedule = {
    apiVersion: "automation.lannonbr.com/v1alpha1", kind: "NodeSchedule", metadata: metadata(name),
    spec: {
      schedule, timeZone,
      scriptConfigMapRef: { name: scriptName },
      env: Object.keys(env).sort().map((key) => ({ name: key, valueFrom: { secretKeyRef: { name: secretName, key } } })),
    },
  };
  return [secret, configMap, nodeSchedule];
}

module.exports = { parseScript, parseEnv, buildResources, validName };
