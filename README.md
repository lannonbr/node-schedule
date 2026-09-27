# NodeSchedule

NodeSchedule runs trusted, dependency-free Node.js scripts on a Kubernetes schedule. Its CLI deploys a script folder as a `NodeSchedule` resource; a single-namespace controller turns each resource into a CronJob.

## How it runs

- The controller watches one namespace (`automation` by default) and creates one same-name CronJob per NodeSchedule.
- Jobs run `script.js` from a read-only ConfigMap volume using a digest-pinned Node 24 Alpine image. Task pods run as non-root without a service-account token or writable root filesystem.
- A missing script ConfigMap, Secret, or Secret key suspends the CronJob and sets `Ready=False`. Status also reports recent Job runs.
- Jobs use `Forbid` concurrency, a 5-minute starting deadline, a 10-minute run deadline, two retries, and 3/3 successful/failed Job history defaults.

The controller intentionally does not expose arbitrary images, commands, volumes, service accounts, or pod templates.

## Local checks

Go 1.23 or newer is required.

```sh
make fmt
make test
make vet
npm test
```

These checks do not require a Kubernetes cluster.

## Build and install

Build and publish a controller image, then install the Helm chart in [`config/`](config). Set `CONTROLLER_IMAGE` to the image you published:

```sh
docker build -t ghcr.io/lannonbr/node-schedule:latest .
docker push ghcr.io/lannonbr/node-schedule:latest
make install CONTROLLER_IMAGE=ghcr.io/lannonbr/node-schedule:latest
```

Use `make install NAMESPACE=my-namespace CONTROLLER_IMAGE=...` to install the controller in another namespace. The controller watches that namespace; deploy scripts there with `nodeschedule deploy <folder> --namespace my-namespace`. Use the same `NAMESPACE` value with `make uninstall`. Uninstall leaves the namespace and cluster-wide CRD in place. The chart also supports `helm upgrade --install nodeschedule ./config --namespace automation --create-namespace --set-string controllerImage=...`; set `nodeImage` with a values file or `--set-string` to override the task image.

Helm installs the CRD on first install but does not upgrade it later. After changing the NodeSchedule API, run `make manifests` and apply `config/crds/automation.lannonbr.com_nodeschedules.yaml` separately to update an existing cluster.

If you previously installed the plain manifests, remove or transfer Helm ownership of the existing Deployment, Role, RoleBinding, and ServiceAccount before the first chart install. Helm will not adopt those resources automatically.

The task image is pinned in `cmd/main.go`. Override it with `NODE_IMAGE` or `--node-image`; the controller requires an `@sha256:` digest.

## Deploy a script

Each folder needs a `script.js` and a local `.env`. The script must export a literal five-field cron schedule or standard `@daily`-style descriptor. It may also export an IANA `timeZone`, which defaults to `Etc/UTC`. The CLI reads these exports without running the script; Node 24 runs it as an ES module. See the [Todoist example](examples/todoist-daily/script.js) for a complete script.

Install the CLI locally with `npm link` (Node 22 or newer), then deploy a folder. `kubectl` must be installed and configured for the target cluster, and the controller and CRD must already be installed. The CLI defaults to the `automation` namespace and the folder name for the resource name.

```sh
nodeschedule deploy examples/todoist-daily
kubectl -n automation get nodeschedule todoist-daily
```

`nodeschedule deploy <folder> --namespace <namespace> --name <name> --context <kube-context>` can override these defaults. The namespace must be watched by a controller installed there. Resources referenced by a manually created NodeSchedule are not adopted by the controller. Every `.env` entry becomes a Secret key referenced by the Job; the script source and resource do not contain those values. `.env` is ignored by Git. The CLI uses server-side apply so Secret data is not duplicated in a last-applied annotation.

The Todoist example fetches today's tasks at 07:00 America/New_York and posts them to Discord. Its `TIME_ZONE` value can also be set in `.env` for the script's date calculation; the exported `timeZone` sets the CronJob schedule.

To run the example immediately after deployment:

```sh
kubectl -n automation create job \
  --from=cronjob/todoist-daily "todoist-daily-manual-$(date +%s)"
```
