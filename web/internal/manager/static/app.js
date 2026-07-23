"use strict";

const app = {
  csrf: "",
  username: "",
  snapshot: null,
  config: null,
  rconSequence: 0,
  rconLines: 0,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];

async function api(path, options = {}) {
  const settings = { ...options, headers: { ...(options.headers || {}) } };
  if (settings.body && typeof settings.body !== "string") {
    settings.headers["Content-Type"] = "application/json";
    settings.body = JSON.stringify(settings.body);
  }
  if ((settings.method || "GET") !== "GET") {
    settings.headers["X-CSRF-Token"] = app.csrf;
  }
  const response = await fetch(path, settings);
  if (response.status === 401) {
    location.href = "/login";
    throw new Error("Session expired");
  }
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const body = await response.json();
      if (body.error) message = body.error;
    } catch (_) {}
    throw new Error(message);
  }
  if (response.status === 204 || response.headers.get("content-length") === "0") return null;
  const type = response.headers.get("content-type") || "";
  return type.includes("application/json") ? response.json() : response.text();
}

function toast(message, error = false) {
  const item = document.createElement("div");
  item.className = `toast${error ? " error" : ""}`;
  item.textContent = message;
  $("#toast-stack").append(item);
  setTimeout(() => item.remove(), 4200);
}

function bytes(value) {
  if (!Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(index >= 3 ? 2 : 1)} ${units[index]}`;
}

function setMeter(name, percent) {
  const safe = Math.max(0, Math.min(100, Number(percent) || 0));
  $(`#${name}-value`).textContent = `${safe.toFixed(1)}%`;
  $(`#${name}-meter`).style.width = `${safe}%`;
}

function renderSnapshot(snapshot) {
  app.snapshot = snapshot;
  setMeter("cpu", snapshot.metrics.cpu_percent);
  setMeter("memory", snapshot.metrics.memory.percent);
  setMeter("swap", snapshot.metrics.swap.percent);
  setMeter("disk", snapshot.metrics.disk.percent);
  $("#memory-detail").textContent = `${bytes(snapshot.metrics.memory.used)} / ${bytes(snapshot.metrics.memory.total)}`;
  $("#swap-detail").textContent = snapshot.metrics.swap.total
    ? `${bytes(snapshot.metrics.swap.used)} / ${bytes(snapshot.metrics.swap.total)}`
    : "No swap available";
  $("#disk-detail").textContent = `${bytes(snapshot.metrics.disk.used)} / ${bytes(snapshot.metrics.disk.total)}`;

  $("#server-dot").classList.toggle("online", snapshot.server_running);
  $("#server-label").textContent = snapshot.server_running ? "Server online" : "Server stopped";
  $("#server-pid").textContent = snapshot.server_running ? `PID ${snapshot.server_pid}` : "PID —";
  $("#pending-banner").classList.toggle("hidden", !snapshot.pending_config);

  const operation = snapshot.operation;
  $("#operation-spinner").classList.toggle("hidden", !operation.running);
  if (operation.action) {
    const state = operation.running ? "running" : operation.successful ? "completed" : "failed";
    $("#operation-title").textContent = `${operation.action} · ${state}`;
    $("#operation-output").textContent = operation.output || (operation.running ? "Operation in progress…" : "No output.");
  }
  $$("[data-server-action]").forEach((button) => {
    const action = button.dataset.serverAction;
    button.disabled = operation.running ||
      (action === "start" && snapshot.server_running) ||
      (action === "stop" && !snapshot.server_running) ||
      (action === "update" && snapshot.server_running);
  });

  const languageSelect = $("#language-select");
  if (!languageSelect.options.length) {
    snapshot.languages.forEach((language) => {
      const option = document.createElement("option");
      option.value = language.code;
      option.textContent = `${language.name} (${language.code})`;
      languageSelect.append(option);
    });
  }
  if (languageSelect.value !== snapshot.language) languageSelect.value = snapshot.language;

  $("#rcon-status").textContent = snapshot.rcon_status;
  $("#rcon-status").classList.toggle("connected", snapshot.rcon_connected);
  appendRconEvents(snapshot.rcon_events || []);
}

function appendRconEvents(events) {
  const consoleElement = $("#rcon-console");
  let added = false;
  for (const event of events) {
    if (event.sequence <= app.rconSequence) continue;
    app.rconSequence = event.sequence;
    const line = document.createElement("div");
    line.className = `console-line ${event.kind}`;
    const timestamp = document.createElement("span");
    timestamp.className = "console-time";
    timestamp.textContent = new Date(event.time).toLocaleTimeString();
    const text = document.createElement("span");
    text.className = "console-text";
    text.textContent = event.text;
    line.append(timestamp, text);
    consoleElement.append(line);
    app.rconLines += 1;
    added = true;
  }
  while (app.rconLines > 400 && consoleElement.firstChild) {
    consoleElement.firstChild.remove();
    app.rconLines -= 1;
  }
  if (added) consoleElement.scrollTop = consoleElement.scrollHeight;
}

async function serverAction(action) {
  try {
    await api("/api/server/action", { method: "POST", body: { action } });
    toast(`${action} operation accepted.`);
  } catch (error) {
    toast(error.message, true);
  }
}

async function loadConfig() {
  const file = $("#config-file").value;
  try {
    app.config = await api(`/api/config?file=${encodeURIComponent(file)}`);
    renderConfig();
  } catch (error) {
    toast(error.message, true);
  }
}

function makeButton(text, className, action) {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = text;
  button.className = className;
  button.addEventListener("click", action);
  return button;
}

async function mutateConfig(mutation) {
  try {
    app.config = await api("/api/config/mutate", {
      method: "POST",
      body: {
        file: app.config.file,
        revision: app.config.revision,
        line: -1,
        section_line: -1,
        section: "",
        key: "",
        value: "",
        enabled: true,
        ...mutation,
      },
    });
    renderConfig();
    toast("Configuration saved.");
  } catch (error) {
    toast(error.message, true);
    if (error.message.includes("reload") || error.message.includes("lifecycle")) await loadConfig();
  }
}

function renderConfig() {
  const target = $("#config-sections");
  target.replaceChildren();
  if (!app.config) return;
  $("#config-stage").textContent = app.config.staged ? "Staged" : "Active";
  $("#config-stage").classList.toggle("staged", app.config.staged);
  $("#discard-config").disabled = !app.config.staged;

  for (const section of app.config.sections) {
    const card = document.createElement("section");
    card.className = "config-section";
    const head = document.createElement("div");
    head.className = "section-head";
    const sectionInput = document.createElement("input");
    sectionInput.value = section.name;
    sectionInput.disabled = section.line < 0;
    sectionInput.setAttribute("aria-label", "Section name");
    head.append(sectionInput);
    if (section.line >= 0) {
      head.append(
        makeButton("Rename", "secondary compact", () =>
          mutateConfig({ action: "rename_section", line: section.line, section: sectionInput.value })),
        makeButton("Remove", "danger compact", () => {
          if (confirm(`Remove [${section.name}] and all lines in that section?`)) {
            mutateConfig({ action: "remove_section", line: section.line });
          }
        }),
      );
    }
    card.append(head);

    const entries = document.createElement("div");
    entries.className = "entry-list";
    for (const entry of section.entries) {
      const row = document.createElement("div");
      row.className = "entry-row";
      row.classList.toggle("disabled", !entry.enabled);
      const key = document.createElement("input");
      key.value = entry.key;
      key.setAttribute("aria-label", "Entry key");
      const value = document.createElement("input");
      value.value = entry.value;
      value.setAttribute("aria-label", "Entry value");
      row.append(
        key,
        value,
        makeButton(entry.enabled ? "Disable" : "Enable", entry.enabled ? "ghost compact" : "primary compact", () =>
          mutateConfig({
            action: "set_entry_enabled",
            line: entry.line,
            section: section.name,
            key: entry.key,
            enabled: !entry.enabled,
          })),
        makeButton("Save", "secondary compact", () =>
          mutateConfig({ action: "set_entry", line: entry.line, key: key.value, value: value.value })),
        makeButton("Remove", "danger compact", () => {
          if (confirm(`Remove ${entry.key}?`)) mutateConfig({ action: "remove_entry", line: entry.line });
        }),
      );
      entries.append(row);
    }
    card.append(entries);

    const add = document.createElement("form");
    add.className = "add-entry";
    const newKey = document.createElement("input");
    newKey.placeholder = "New key";
    newKey.required = true;
    const newValue = document.createElement("input");
    newValue.placeholder = "Value";
    const submit = document.createElement("button");
    submit.type = "submit";
    submit.className = "primary compact";
    submit.textContent = "Add item";
    add.append(newKey, newValue, submit);
    add.addEventListener("submit", (event) => {
      event.preventDefault();
      mutateConfig({
        action: "add_entry",
        section_line: section.line,
        key: newKey.value,
        value: newValue.value,
      });
    });
    card.append(add);
    target.append(card);
  }
}

async function loadAccounts() {
  try {
    const data = await api("/api/accounts");
    renderAccounts(data.accounts);
  } catch (error) {
    toast(error.message, true);
  }
}

function renderAccounts(accounts) {
  const list = $("#account-list");
  list.replaceChildren();
  for (const account of accounts) {
    const row = document.createElement("div");
    row.className = "list-row";
    const username = document.createElement("input");
    username.value = account.username;
    username.setAttribute("aria-label", `Username for ${account.username}`);
    const password = document.createElement("input");
    password.type = "password";
    password.placeholder = "New password (optional)";
    password.autocomplete = "new-password";
    const save = makeButton("Save", "secondary compact", async () => {
      try {
        await api("/api/accounts/edit", {
          method: "POST",
          body: { old_username: account.username, username: username.value, password: password.value },
        });
        toast("Account updated. Its sessions were signed out.");
        if (account.username === app.username) {
          location.href = "/login";
          return;
        }
        loadAccounts();
      } catch (error) {
        toast(error.message, true);
      }
    });
    const remove = makeButton("Delete", "danger compact", async () => {
      if (!confirm(`Delete account ${account.username}?`)) return;
      try {
        await api("/api/accounts/delete", { method: "POST", body: { username: account.username } });
        toast("Account deleted.");
        if (account.username === app.username) {
          location.href = "/login";
          return;
        }
        loadAccounts();
      } catch (error) {
        toast(error.message, true);
      }
    });
    remove.disabled = accounts.length < 2;
    row.append(username, password, save, remove);
    list.append(row);
  }
}

async function loadAccess() {
  try {
    const data = await api("/api/access");
    $("#base-policy").value = data.config.base_policy;
    $("#current-ip").textContent = `Current client address: ${data.current_ip}`;
    renderAccessRules(data.config.rules);
  } catch (error) {
    toast(error.message, true);
  }
}

async function loadServices() {
  try {
    const settings = await api("/api/services");
    $("#mordhau-service-mode").value = settings.mordhau_automatic ? "automatic" : "manual";
    $("#web-service-mode").value = settings.web_automatic ? "automatic" : "manual";
    $("#web-service-port").value = settings.web_port;
  } catch (error) {
    toast(error.message, true);
  }
}

async function setServiceMode(service, value) {
  try {
    await api("/api/services/mode", {
      method: "POST",
      body: { service, automatic: value === "automatic" },
    });
    toast(`${service === "web" ? "Web" : "MORDHAU"} boot mode saved.`);
    loadServices();
  } catch (error) {
    toast(error.message, true);
    loadServices();
  }
}

function renderAccessRules(rules) {
  rules = Array.isArray(rules) ? rules : [];
  const list = $("#access-rules");
  list.replaceChildren();
  if (!rules.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No explicit network rules.";
    list.append(empty);
    return;
  }
  for (const rule of rules) {
    const row = document.createElement("div");
    row.className = "list-row";
    const action = document.createElement("select");
    action.innerHTML = '<option value="allow">Allow</option><option value="deny">Deny</option>';
    action.value = rule.action;
    const network = document.createElement("input");
    network.value = rule.network;
    const save = makeButton("Save", "secondary compact", async () => {
      try {
        await api("/api/access/rule", {
          method: "POST",
          body: { id: rule.id, action: action.value, network: network.value },
        });
        toast("Access rule saved.");
        loadAccess();
      } catch (error) {
        toast(error.message, true);
      }
    });
    const remove = makeButton("Delete", "danger compact", async () => {
      if (!confirm(`Delete ${rule.action} ${rule.network}?`)) return;
      try {
        await api("/api/access/rule/delete", { method: "POST", body: { id: rule.id } });
        toast("Access rule deleted.");
        loadAccess();
      } catch (error) {
        toast(error.message, true);
      }
    });
    if (rule.temporary) {
      action.disabled = true;
      network.disabled = true;
      save.disabled = true;
      const expiry = rule.expires_at ? new Date(rule.expires_at).toLocaleString() : "soon";
      save.textContent = `Emergency · expires ${expiry}`;
      save.className = "ghost compact temporary-label";
    }
    row.append(action, network, save, remove);
    list.append(row);
  }
}

function bindEvents() {
  $$(".tab").forEach((tab) => tab.addEventListener("click", () => {
    $$(".tab").forEach((item) => item.classList.toggle("active", item === tab));
    $$(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === `panel-${tab.dataset.panel}`));
  }));
  $$("[data-server-action]").forEach((button) =>
    button.addEventListener("click", () => serverAction(button.dataset.serverAction)));

  $("#language-select").addEventListener("change", async (event) => {
    try {
      await api("/api/language", { method: "POST", body: { language: event.target.value } });
      toast("Launch language saved.");
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#logout-button").addEventListener("click", async () => {
    try { await api("/logout", { method: "POST" }); } finally { location.href = "/login"; }
  });
  $("#config-file").addEventListener("change", loadConfig);
  $("#discard-config").addEventListener("click", async () => {
    if (!confirm("Discard all staged Game.ini and Engine.ini changes?")) return;
    try {
      await api("/api/config/discard", { method: "POST" });
      toast("Staged configuration discarded.");
      loadConfig();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#add-section-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const input = $("#new-section-name");
    await mutateConfig({ action: "add_section", section: input.value });
    input.value = "";
  });
  $("#new-account-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/accounts/create", {
        method: "POST",
        body: { username: $("#new-account-name").value, password: $("#new-account-password").value },
      });
      event.target.reset();
      toast("Account created.");
      loadAccounts();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#base-policy").addEventListener("change", async (event) => {
    const policy = event.target.value;
    if (policy === "all_deny" &&
        !confirm("Switch to deny all? Your current IP will receive a temporary 30-minute emergency allow rule.")) {
      loadAccess();
      return;
    }
    try {
      await api("/api/access/base", { method: "POST", body: { policy } });
      toast("Base access policy saved.");
      loadAccess();
    } catch (error) {
      toast(error.message, true);
      loadAccess();
    }
  });
  $("#new-rule-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/access/rule", {
        method: "POST",
        body: { id: "", action: $("#new-rule-action").value, network: $("#new-rule-network").value },
      });
      $("#new-rule-network").value = "";
      toast("Access rule added.");
      loadAccess();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#mordhau-service-mode").addEventListener("change", (event) =>
    setServiceMode("mordhau", event.target.value));
  $("#web-service-mode").addEventListener("change", (event) =>
    setServiceMode("web", event.target.value));
  $("#web-port-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/services/web-port", {
        method: "POST",
        body: { port: Number($("#web-service-port").value) },
      });
      toast("Web service boot port saved.");
      loadServices();
    } catch (error) {
      toast(error.message, true);
    }
  });
}

async function initialize() {
  bindEvents();
  try {
    const me = await api("/api/me");
    app.csrf = me.csrf;
    app.username = me.username;
    $("#current-user").textContent = `${me.username} · ${me.current_ip}`;
    const snapshot = await api("/api/snapshot");
    renderSnapshot(snapshot);
    await Promise.all([loadConfig(), loadAccounts(), loadAccess(), loadServices()]);
    const stream = new EventSource("/api/events");
    stream.addEventListener("snapshot", (event) => {
      try { renderSnapshot(JSON.parse(event.data)); } catch (_) {}
    });
    stream.onerror = () => $("#rcon-status").textContent = "Live stream reconnecting";
  } catch (error) {
    toast(error.message, true);
  }
}

initialize();
