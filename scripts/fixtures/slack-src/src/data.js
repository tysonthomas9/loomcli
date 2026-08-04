export const teams = [
  { id: "aether", name: "Aether" },
  { id: "loom", name: "Loom" },
];

export const users = [
  { id: "u1", name: "Nova Chen", role: "Design lead" },
  { id: "u2", name: "Atlas Reed", role: "Frontend engineer" },
  { id: "u3", name: "Mira Singh", role: "Product manager" },
];

export const channels = [
  {
    id: "c1",
    teamId: "aether",
    name: "general",
    topic: "Daily product coordination",
    unread: 3,
  },
  {
    id: "c2",
    teamId: "aether",
    name: "design",
    topic: "Interface reviews and polish",
    unread: 0,
  },
  {
    id: "c3",
    teamId: "loom",
    name: "release",
    topic: "Release readiness",
    unread: 7,
  },
];

export const messages = [
  {
    id: "m1",
    channelId: "c1",
    userId: "u1",
    time: "9:12 AM",
    text: "The shell is ready for channel, search, and workspace workflows.",
  },
  {
    id: "m2",
    channelId: "c1",
    userId: "u2",
    time: "9:18 AM",
    text: "I am tightening the sidebar states before the next pass.",
  },
  {
    id: "m3",
    channelId: "c1",
    userId: "u3",
    time: "9:24 AM",
    text: "Please keep the empty states useful for first-run users.",
  },
];
