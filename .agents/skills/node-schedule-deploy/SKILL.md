---
name: node-schedule-deploy
description: Used to deploy simple nodejs scripts to Kubernetes on cron schedules.
---

This skill uses the nodeschedule CLI to deploy a nodejs script as a cronjob to a Kubernetes cluster.

To start, the folder for a script needs to have a script.js file which is the script that you want to schedule as well as a .env file which will be any secrets or env vars you want to provide to the script.

In script.js, it needs at least 1 export of `export const schedule = <cron_string>` to tell Kubernetes when the cronjob should run. It can also have an optional `export const timezone` which will be a IANA timezone. If not provided, the timezone used will be UTC+0.

Then once all of that is in place, run the following to deploy it:

```sh
nodeschedule deploy <folder>
```

There should now be a new node schedule with the name of the folder if you run `kubectl get nodeschedules.automation.lannonbr.com -n automation`
