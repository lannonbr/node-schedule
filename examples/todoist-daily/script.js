export const schedule = "0 7 * * *";
export const timeZone = "America/New_York";

const required = (name) => {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
};

const localDate = (timeZone) => {
  const parts = new Intl.DateTimeFormat("en", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(new Date());
  const value = Object.fromEntries(parts.map(({ type, value }) => [type, value]));
  return `${value.year}-${value.month}-${value.day}`;
};

const main = async () => {
  const todoistToken = required("TODOIST_API_TOKEN");
  const webhookUrl = required("DISCORD_WEBHOOK_URL");
  const today = localDate(process.env.TIME_ZONE || "America/New_York");

  const todoistResponse = await fetch("https://api.todoist.com/api/v1/tasks", {
    headers: { Authorization: `Bearer ${todoistToken}` },
  });
  if (!todoistResponse.ok) {
    throw new Error(`Todoist returned ${todoistResponse.status}`);
  }

  const payload = await todoistResponse.json();
  const tasks = payload.results ?? payload;
  const dueToday = tasks.filter((task) => task.due?.date === today);
  const lines = dueToday.length
    ? dueToday.map((task) => `• ${task.content}`).join("\n")
    : "• Nothing due today 🎉";

  const discordResponse = await fetch(webhookUrl, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ content: `**Todoist — ${today}**\n${lines}` }),
  });
  if (!discordResponse.ok) {
    throw new Error(`Discord returned ${discordResponse.status}`);
  }

  console.log(`Posted ${dueToday.length} task(s) due ${today}.`);
};

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
