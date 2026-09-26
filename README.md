# NodeSchedule

NodeSchedule is a small, single-namespace Kubernetes controller for trusted,
dependency-free Node.js scripts. A `NodeSchedule` references a ConfigMap with a
`script.js` key; the controller validates its local ConfigMap and Secret
dependencies and reconciles a native CronJob.

This repository contains an `automation.lannonbr.com/v1alpha1` controller and a
CLI that deploys a script folder as a NodeSchedule.

## Implemented behavior

- Watches one namespace (`automation` by default).
- Creates one same-name CronJob with an owner reference for each NodeSchedule.
- Runs `node /scripts/script.js` from a read-only ConfigMap volume.
- Uses a digest-pinned Node 24 Alpine image configured at controller startup.
- Applies `Forbid` concurrency, a 5-minute starting deadline, a 10-minute run
  deadline, two retries, and 3/3 successful/failed Job history defaults.
- Disables the task pod's service-account token and applies a non-root,
  read-only-root-filesystem security context.
- Suspends the CronJob and publishes `Ready=False` when a referenced ConfigMap,
  `script.js`, Secret, or Secret key is missing.
- Requeues promptly for NodeSchedule, CronJob, Job, referenced ConfigMap, and
  referenced Secret changes.
- Reports the generated CronJob and recent Job phase/times in status and emits
  Kubernetes Events.

The controller intentionally does not expose arbitrary images, commands,
volumes, service accounts, or pod templates.

## Local checks

Go 1.23 or newer is required.

```sh
make fmt
make test
make vet
npm test
```

The Go unit tests build the generated CronJob in memory. The CLI tests use a
stubbed `kubectl`; no Kubernetes cluster is required.

## Build and install

Build and publish a controller image, then install its plain Kubernetes
manifests. Set `CONTROLLER_IMAGE` to the image you published:

```sh
docker build -t ghcr.io/lannonbr/node-schedule:latest .
docker push ghcr.io/lannonbr/node-schedule:latest
make install CONTROLLER_IMAGE=ghcr.io/lannonbr/node-schedule:latest
```

The default task runtime is the multi-platform official Node image
`node:24-alpine` pinned to the digest in `cmd/main.go`. Override it with
`NODE_IMAGE` or `--node-image`; startup rejects a value that is not pinned with
`@sha256:`.

## Deploy a script

Each folder has a `script.js` and a local `.env`. Export a literal five-field
cron schedule (or a standard `@daily`-style descriptor) from the script. The
optional `timeZone` export uses an IANA time zone; it defaults to `Etc/UTC`.
The CLI reads these exports without running or changing the script. Node 24
runs the exported `script.js` as an ES module.

```js
export const schedule = "0 7 * * *";
export const timeZone = "America/New_York";

const webhook = process.env.DISCORD_WEBHOOK_URL;
// Your script runs at the scheduled time.
```

```dotenv
DISCORD_WEBHOOK_URL=https://example.com/webhook
API_TOKEN=replace-me
```

Install the CLI locally with `npm link` (Node 22 or newer), then deploy a
folder. `kubectl` must be installed and configured for the target cluster, and
the controller and CRD must already be installed. The CLI defaults to the
`automation` namespace and the folder name for the resource name.

```sh
npm link
cat > examples/todoist-daily/.env <<'EOF'
TODOIST_API_TOKEN=replace-me
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/replace-me
EOF
chmod 600 examples/todoist-daily/.env
nodeschedule deploy examples/todoist-daily
kubectl -n automation get nodeschedule todoist-daily
```

`nodeschedule deploy <folder> --namespace <namespace> --name <name>
--context <kube-context>` can override these defaults. The namespace must be
watched by a controller installed there. Each deploy applies a
same-name ConfigMap, Secret, and NodeSchedule. Every `.env` entry becomes a
Secret key referenced by the Job; the script source and resource do not contain
those values. `.env` is ignored by Git. The CLI uses server-side apply so
Secret data is not duplicated in a last-applied annotation.

The [Todoist example](examples/todoist-daily) fetches today's tasks at 07:00
America/New_York and posts them to Discord. Its `TIME_ZONE` value can also be
set in `.env` for the script's date calculation; the exported `timeZone` sets
the CronJob schedule.

To run the example immediately after deployment:

```sh
kubectl -n automation create job \
  --from=cronjob/todoist-daily "todoist-daily-manual-$(date +%s)"
```
