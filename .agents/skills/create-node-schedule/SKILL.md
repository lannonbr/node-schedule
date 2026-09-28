---
name: create-node-schedule
description: Used to create simple nodejs scripts to eventually be deployed Kubernetes on cron schedules.
---

This skill generates Node.js scripts for nodeschedule to eventually deploy.

To start, create a folder wth a name based on what the user prompts. In this folder, generate a script.js file which is the script that you want to schedule as well as a .env file which will be any secrets or env vars you want to provide to the script.

In script.js, it needs at least 1 export of `export const schedule = <cron_string>` to tell Kubernetes when the cronjob should run. It can also have an optional `export const timezone` which will be a IANA timezone. If not provided, the timezone used will be UTC+0.

Note this skill doesn't explicitly deploy the script, just preps it in order to be deployed via another skill, node-schedule-deploy.
