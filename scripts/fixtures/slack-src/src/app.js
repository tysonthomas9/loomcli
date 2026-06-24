import { channels, messages, teams, users } from "./data.js";

const app = document.querySelector("#app");
const activeTeam = teams[0];
const activeChannel = channels.find((channel) => channel.teamId === activeTeam.id);
const channelMessages = messages.filter(
  (message) => message.channelId === activeChannel.id,
);

function userName(userId) {
  return users.find((user) => user.id === userId)?.name ?? "Unknown";
}

function renderChannel(channel) {
  const unread = channel.unread > 0 ? `<span>${channel.unread}</span>` : "";
  return `<button class="channel ${channel.id === activeChannel.id ? "active" : ""}">
    <span># ${channel.name}</span>${unread}
  </button>`;
}

function renderMessage(message) {
  return `<article class="message">
    <div class="avatar">${userName(message.userId).slice(0, 1)}</div>
    <div>
      <header><strong>${userName(message.userId)}</strong><time>${message.time}</time></header>
      <p>${message.text}</p>
    </div>
  </article>`;
}

app.innerHTML = `
  <main class="slack-shell">
    <aside class="team-rail" aria-label="Teams">
      ${teams.map((team) => `<button>${team.name.slice(0, 1)}</button>`).join("")}
    </aside>
    <nav class="channel-sidebar" aria-label="Channels">
      <div>
        <p class="eyebrow">Workspace</p>
        <h1>${activeTeam.name}</h1>
      </div>
      <section>
        <h2>Channels</h2>
        ${channels
          .filter((channel) => channel.teamId === activeTeam.id)
          .map(renderChannel)
          .join("")}
      </section>
    </nav>
    <section class="conversation" aria-label="Conversation">
      <header class="conversation-header">
        <div>
          <p class="eyebrow"># ${activeChannel.name}</p>
          <h2>${activeChannel.topic}</h2>
        </div>
        <label class="search-box">
          <span>Search</span>
          <input placeholder="Search messages" />
        </label>
      </header>
      <div class="message-list">${channelMessages.map(renderMessage).join("")}</div>
      <form class="composer">
        <input aria-label="Message" placeholder="Message #${activeChannel.name}" />
        <button type="submit">Send</button>
      </form>
    </section>
  </main>
`;
