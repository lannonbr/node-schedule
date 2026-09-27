#!/usr/bin/env node

const { readFile, realpath } = require("node:fs/promises");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { parseScript, parseEnv, buildResources, validName } = require("./resources");

function usage() {
  return "Usage: nodeschedule deploy <folder> [--namespace <name>] [--name <name>] [--context <context>]";
}

function parseArgs(args) {
  if (args[0] !== "deploy" || !args[1] || args[1].startsWith("-")) {
    throw new Error(usage());
  }
  const options = { folder: args[1], namespace: "automation" };
  for (let i = 2; i < args.length; i += 2) {
    const flag = args[i];
    const value = args[i + 1];
    if (!value || value.startsWith("--")) throw new Error(`Missing value for ${flag}\n${usage()}`);
    if (flag === "--namespace") options.namespace = value;
    else if (flag === "--name") options.name = value;
    else if (flag === "--context") options.context = value;
    else throw new Error(`Unknown option ${flag}\n${usage()}`);
  }
  return options;
}

function kubectlApply(resource, namespace, context, outputJSON = false) {
  return new Promise((resolve, reject) => {
    const args = ["apply", "--server-side", "--field-manager=nodeschedule-cli", "-n", namespace];
    if (context) args.push("--context", context);
    if (outputJSON) args.push("-o", "json");
    args.push("-f", "-");
    const child = spawn("kubectl", args, { stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += chunk; });
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    child.on("error", (error) => reject(new Error(`Cannot run kubectl: ${error.message}`)));
    child.on("close", (code) => {
      if (code === 0) resolve(stdout.trim());
      else reject(new Error(`kubectl apply failed for ${resource.kind} ${resource.metadata.name}: ${stderr.trim() || `exit code ${code}`}`));
    });
    child.stdin.on("error", () => {}); // A failed kubectl process may close stdin early.
    child.stdin.end(JSON.stringify(resource));
  });
}

async function deploy(options) {
  const folder = await realpath(options.folder);
  const name = options.name || path.basename(folder);
  if (!validName(name)) throw new Error(`Invalid resource name ${JSON.stringify(name)}; use a lowercase Kubernetes DNS label or --name`);
  if (!validName(options.namespace)) throw new Error(`Invalid namespace ${JSON.stringify(options.namespace)}`);

  const script = await readFile(path.join(folder, "script.js"), "utf8");
  const envFile = await readFile(path.join(folder, ".env"), "utf8");
  const { source, schedule, timeZone } = parseScript(script);
  const env = parseEnv(envFile);
  const [nodeSchedule, ...dependents] = buildResources({ name, namespace: options.namespace, source, schedule, timeZone, env });
  const applied = await kubectlApply(nodeSchedule, options.namespace, options.context, true);
  let uid;
  try { uid = JSON.parse(applied).metadata.uid; }
  catch { throw new Error(`kubectl did not return a valid NodeSchedule ${options.namespace}/${name}`); }
  if (typeof uid !== "string" || !uid) throw new Error(`kubectl did not return a UID for NodeSchedule ${options.namespace}/${name}`);

  for (const resource of dependents) {
    resource.metadata.ownerReferences = [{
      apiVersion: nodeSchedule.apiVersion, kind: nodeSchedule.kind, name, uid,
    }];
    const result = await kubectlApply(resource, options.namespace, options.context);
    console.log(result);
  }
  console.log(`Deployed NodeSchedule ${options.namespace}/${name} (${schedule}, ${timeZone})`);
}

if (require.main === module) {
  deploy(parseArgs(process.argv.slice(2))).catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}

module.exports = { parseArgs, deploy };
