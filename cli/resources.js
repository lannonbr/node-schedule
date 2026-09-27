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
  if (!validSchedule(schedule)) {
    throw new Error(`Invalid cron schedule ${JSON.stringify(schedule)}; use a valid five-field expression or standard @ descriptor`);
  }
  const timeZone = zone || "Etc/UTC";
  try { new Intl.DateTimeFormat("en", { timeZone }); }
  catch { throw new Error(`Invalid time zone ${JSON.stringify(timeZone)}`); }
  return { source: script, schedule, timeZone };
}

function validSchedule(schedule) {
  const expression = schedule.trim();
  if (/^@(yearly|annually|monthly|weekly|daily|midnight|hourly)$/.test(expression)) return true;
  const fields = expression.split(/\s+/);
  if (fields.length !== 5) return false;
  const ranges = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 6]];
  const names = [null, null, null,
    ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"],
    ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"]];
  return fields.every((field, index) => field.split(",").every((part) => {
    const match = /^(\*|\?|[A-Za-z0-9]+(?:-[A-Za-z0-9]+)?)(?:\/(\d+))?$/.exec(part);
    if (!match || (match[1] === "?" && index !== 2 && index !== 4) ||
        (match[2] !== undefined && (!Number.isSafeInteger(Number(match[2])) || Number(match[2]) < 1))) return false;
    if (match[1] === "*" || match[1] === "?") return true;
    const bounds = match[1].split("-").map((value) => {
      const nameIndex = names[index]?.indexOf(value.toUpperCase()) ?? -1;
      return nameIndex >= 0 ? nameIndex + (index === 3 ? 1 : 0) :
        /^\d+$/.test(value) ? Number(value) : NaN;
    });
    return bounds.every((value) => Number.isInteger(value) && value >= ranges[index][0] && value <= ranges[index][1]) &&
      (bounds.length === 1 || bounds[0] <= bounds[1]);
  }));
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
  return [nodeSchedule, secret, configMap];
}

module.exports = { parseScript, parseEnv, buildResources, validName };
