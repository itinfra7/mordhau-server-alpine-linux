"use strict";

const app = {
  csrf: "",
  username: "",
  snapshot: null,
  config: null,
  modPlan: null,
  modPlanReference: "",
  modsLoading: false,
  modsReloadRequested: false,
  modRevision: null,
  rconSequence: 0,
  rconLines: 0,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const themeStorageKey = "mordhau-control-theme";
const minimumModRefreshMinutes = 1;
const maximumModRefreshMinutes = 10080;

function setTheme(theme, persist = true) {
  const dark = theme === "dark";
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  const toggle = $("#theme-toggle");
  if (toggle) {
    toggle.setAttribute("aria-pressed", String(dark));
    toggle.setAttribute("aria-label", dark ? "Switch to light mode" : "Switch to dark mode");
    $("#theme-toggle-icon").textContent = dark ? "☀" : "☾";
    $("#theme-toggle-label").textContent = dark ? "Light mode" : "Dark mode";
  }
  if (persist) {
    try {
      localStorage.setItem(themeStorageKey, dark ? "dark" : "light");
    } catch (_) {}
  }
}

function initializeTheme() {
  setTheme(document.documentElement.dataset.theme === "dark" ? "dark" : "light", false);
}

function clearLegacyModRefreshPreference() {
  try {
    localStorage.removeItem("mordhau-mod-refresh-minutes");
  } catch (_) {}
}

function validModRefreshMinutes(value) {
  const minutes = Number(value);
  return Number.isInteger(minutes) &&
    minutes >= minimumModRefreshMinutes &&
    minutes <= maximumModRefreshMinutes
    ? minutes
    : null;
}

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
  if (Number.isSafeInteger(snapshot.mod_revision)) {
    const changed = app.modRevision !== null &&
      app.modRevision !== snapshot.mod_revision;
    app.modRevision = snapshot.mod_revision;
    if (changed) loadMods();
  }
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
  $("#rcon-message-submit").disabled = operation.running || !snapshot.server_running;
  $("#rcon-message").disabled = operation.running || !snapshot.server_running;
  appendRconEvents(snapshot.rcon_events || []);
}

function appendRconEvents(events) {
  const consoleElement = $("#rcon-console");
  let added = false;
  for (const event of events) {
    if (event.sequence <= app.rconSequence) continue;
    app.rconSequence = event.sequence;
    if (event.kind === "system" &&
        (event.text === "RCON connected; all broadcasts enabled" ||
         event.text === "RCON reconnected with the running server's previous settings; all broadcasts enabled" ||
         event.text.startsWith("RCON connection closed:"))) {
      continue;
    }
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

async function sendUnicodeMessage(event) {
  event.preventDefault();
  const input = $("#rcon-message");
  const submit = $("#rcon-message-submit");
  submit.disabled = true;
  try {
    await api("/api/rcon/message", {
      method: "POST",
      body: { message: input.value },
    });
    input.value = "";
    input.focus();
    toast("Message sent.");
  } catch (error) {
    toast(error.message, true);
  } finally {
    const snapshot = app.snapshot;
    submit.disabled = !snapshot || snapshot.operation.running || !snapshot.server_running;
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
    card.classList.toggle("disabled", section.enabled === false);
    const head = document.createElement("div");
    head.className = "section-head";
    const sectionInput = document.createElement("input");
    sectionInput.value = section.name;
    const globalSection = section.line < 0 && !section.id &&
      section.name === "(entries before first section)";
    sectionInput.disabled = globalSection;
    sectionInput.autocapitalize = "none";
    sectionInput.spellcheck = false;
    sectionInput.setAttribute("aria-label", "Section name");
    head.append(sectionInput);
    if (!globalSection) {
      head.append(
        makeButton("Rename", "secondary compact", () =>
          mutateConfig({
            action: "rename_section",
            line: section.line,
            section_id: section.id || "",
            section: sectionInput.value,
          })),
        makeButton(
          section.enabled === false ? "Enable section" : "Disable section",
          section.enabled === false ? "primary compact" : "ghost compact",
          () => mutateConfig({
            action: "set_section_enabled",
            line: section.line,
            section_id: section.id || "",
            section: section.name,
            enabled: section.enabled === false,
          }),
        ),
        makeButton("Remove", "danger compact", () => {
          if (confirm(`Remove [${section.name}] and all active or disabled items in that section?`)) {
            mutateConfig({
              action: "remove_section",
              line: section.line,
              section_id: section.id || "",
              section: section.name,
            });
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
      key.autocapitalize = "none";
      key.spellcheck = false;
      key.setAttribute("aria-label", "Entry key");
      const value = document.createElement("input");
      value.value = entry.value;
      value.autocapitalize = "none";
      value.spellcheck = false;
      value.setAttribute("aria-label", "Entry value");
      row.append(
        key,
        value,
        makeButton(entry.enabled ? "Disable" : "Enable", entry.enabled ? "ghost compact" : "primary compact", () =>
          mutateConfig({
            action: "set_entry_enabled",
            line: entry.line,
            entry_id: entry.id || "",
            section: section.name,
            key: entry.key,
            enabled: !entry.enabled,
          })),
        makeButton("Save", "secondary compact", () =>
          mutateConfig({
            action: "set_entry",
            line: entry.line,
            entry_id: entry.id || "",
            section: section.name,
            key: key.value,
            value: value.value,
          })),
        makeButton("Remove", "danger compact", () => {
          if (confirm(`Remove ${entry.key}?`)) {
            mutateConfig({
              action: "remove_entry",
              line: entry.line,
              entry_id: entry.id || "",
              section: section.name,
              key: entry.key,
            });
          }
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
    newKey.autocapitalize = "none";
    newKey.spellcheck = false;
    const newValue = document.createElement("input");
    newValue.placeholder = "Value";
    newValue.autocapitalize = "none";
    newValue.spellcheck = false;
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
        section_id: section.id || "",
        section: section.name,
        key: newKey.value,
        value: newValue.value,
      });
    });
    card.append(add);
    target.append(card);
  }
}

function modDisplayName(item) {
  return item && item.name ? item.name : `Resource ID ${item ? item.id : "—"}`;
}

function formatModDate(seconds) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "";
  return new Date(seconds * 1000).toLocaleString();
}

function formatBrowserDate(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  let zone = "";
  try {
    zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch (_) {}
  let formatted = "";
  try {
    formatted = date.toLocaleString(undefined, {
      dateStyle: "medium",
      timeStyle: "medium",
    });
  } catch (_) {
    formatted = date.toLocaleString();
  }
  return zone ? `${formatted} (${zone})` : formatted;
}

function renderModRefresh(refresh) {
  const minutes = validModRefreshMinutes(refresh && refresh.interval_minutes) || 60;
  $("#mods-refresh-minutes").value = String(minutes);

  const status = $("#mods-refresh-status");
  const lastSuccess = formatBrowserDate(refresh && refresh.last_success_at);
  const nextRefresh = formatBrowserDate(refresh && refresh.next_refresh_at);
  const failed = Boolean(refresh && refresh.last_error);
  const parts = [`Server interval: ${minutes} min`];
  parts.push(lastSuccess
    ? `Last successful refresh: ${lastSuccess}`
    : "No successful refresh yet");
  if (refresh && refresh.refreshing) {
    parts.push("Refreshing now");
  } else if (nextRefresh) {
    parts.push(`${failed ? "Next retry" : "Next refresh"}: ${nextRefresh}`);
  }
  if (failed) parts.push(`Last attempt failed: ${refresh.last_error}`);
  status.textContent = parts.join(" · ");
  status.classList.toggle("error", failed);
}

function renderModIOSettings(settings) {
  const configured = Boolean(settings && settings.api_key_configured);
  const status = $("#modio-key-status");
  status.textContent = configured
    ? `${settings.game_name || "MORDHAU"} · game ${settings.game_id}`
    : "Not configured";
  status.classList.toggle("connected", configured);
  $("#modio-api-base").value = settings && settings.api_base
    ? settings.api_base
    : "https://api.mod.io/v1";
  $("#modio-api-key").placeholder = configured
    ? "Saved · enter only to replace"
    : "32-character key";
  $("#modio-clear").disabled = !configured;
}

function appendConfiguredModDependencies(details, configured, configuredByID) {
  const section = document.createElement("div");
  section.className = "mod-dependencies";

  const dependencies = Array.isArray(configured.dependencies)
    ? configured.dependencies
    : [];
  const unresolved = new Set(
    Array.isArray(configured.unresolved_dependencies)
      ? configured.unresolved_dependencies
      : [],
  );
  const heading = document.createElement("strong");
  heading.className = "mod-dependencies-heading";
  heading.textContent = configured.dependencies_checked
    ? `Dependencies (${dependencies.length})`
    : "Dependencies";
  section.append(heading);

  if (!configured.metadata) {
    const note = document.createElement("p");
    note.className = "mod-dependency-note";
    note.textContent = "Unavailable — mod.io metadata was not returned for this Resource ID.";
    section.append(note);
    details.append(section);
    return;
  }

  if (configured.dependency_error) {
    const warning = document.createElement("p");
    warning.className = configured.enabled
      ? "mod-dependency-warning"
      : "mod-dependency-note";
    warning.textContent = `Dependency check failed: ${configured.dependency_error}`;
    section.append(warning);
    details.append(section);
    return;
  }

  if (!configured.dependencies_checked) {
    const note = document.createElement("p");
    note.className = "mod-dependency-note";
    note.textContent = "Unavailable — configure a valid mod.io API key to inspect dependencies.";
    section.append(note);
    details.append(section);
    return;
  }

  if (!dependencies.length) {
    const note = document.createElement("p");
    note.className = "mod-dependency-note";
    note.textContent = "None reported by mod.io.";
    section.append(note);
    details.append(section);
    return;
  }

  if (configured.enabled && unresolved.size) {
    const warning = document.createElement("p");
    warning.className = "mod-dependency-warning";
    const ids = [...unresolved].map((id) => `Mods=${id}`).join(", ");
    warning.textContent = `Warning: ${unresolved.size} required dependenc${unresolved.size === 1 ? "y is" : "ies are"} not enabled: ${ids}`;
    section.append(warning);
  }

  const list = document.createElement("ul");
  list.className = "dependency-list configured-dependency-list";
  for (const dependency of dependencies) {
    const item = document.createElement("li");
    const configuredDependency = configuredByID.get(dependency.id);
    const enabled = Boolean(configuredDependency && configuredDependency.enabled);
    const shouldWarn = configured.enabled && unresolved.has(dependency.id);
    item.classList.toggle("unresolved", shouldWarn);

    const identity = document.createElement("span");
    identity.className = "dependency-identity";
    const name = document.createElement("span");
    name.textContent = modDisplayName(dependency);
    const id = document.createElement("code");
    id.textContent = `Mods=${dependency.id}`;
    identity.append(name, id);

    const status = document.createElement("span");
    status.className = `dependency-status${enabled ? " resolved" : shouldWarn ? " unresolved" : ""}`;
    status.textContent = enabled
      ? "enabled"
      : configuredDependency
        ? "disabled"
        : "not configured";
    item.append(identity, status);
    list.append(item);
  }
  section.append(list);
  details.append(section);
}

function renderConfiguredMods(data) {
  if (Number.isSafeInteger(data.revision)) app.modRevision = data.revision;
  renderModRefresh(data.refresh || {});
  renderModIOSettings(data.settings || {});
  const stage = $("#mods-config-stage");
  stage.textContent = data.config_staged ? "Staged" : "Active";
  stage.classList.toggle("staged", Boolean(data.config_staged));

  const apiError = $("#mods-api-error");
  apiError.classList.toggle("hidden", !data.api_error);
  apiError.textContent = data.api_error
    ? `Metadata lookup failed: ${data.api_error}`
    : "";

  const invalid = $("#mods-invalid-warning");
  invalid.classList.toggle("hidden", !data.invalid_entries);
  invalid.textContent = data.invalid_entries
    ? `${data.invalid_entries} invalid Mods= entr${data.invalid_entries === 1 ? "y was" : "ies were"} found. Use INI configuration to correct it.`
    : "";

  const list = $("#configured-mods");
  list.replaceChildren();
  const mods = Array.isArray(data.mods) ? data.mods : [];
  const configuredByID = new Map(mods.map((item) => [item.id, item]));
  if (!mods.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No Mods= entries are configured in the MORDHAU game session section.";
    list.append(empty);
    return;
  }

  for (const configured of mods) {
    const metadata = configured.metadata;
    const row = document.createElement("div");
    row.className = "mod-row";
    row.classList.toggle("disabled", !configured.enabled);

    const details = document.createElement("div");
    details.className = "mod-details";
    const title = document.createElement("div");
    title.className = "mod-title";
    const name = document.createElement("strong");
    name.textContent = metadata ? modDisplayName(metadata) : `Resource ID ${configured.id}`;
    const id = document.createElement("span");
    id.className = "mod-id";
    id.textContent = `Mods=${configured.id}`;
    title.append(name, id);
    details.append(title);

    if (metadata && metadata.summary) {
      const summary = document.createElement("p");
      summary.className = "mod-summary";
      summary.textContent = metadata.summary;
      summary.title = metadata.summary;
      details.append(summary);
    }
    const meta = document.createElement("div");
    meta.className = "mod-meta";
    const fields = [];
    if (metadata && metadata.modfile && metadata.modfile.version) {
      fields.push(`version ${metadata.modfile.version}`);
    }
    if (metadata && metadata.date_updated) {
      fields.push(`updated ${formatModDate(metadata.date_updated)}`);
    }
    if (configured.occurrences > 1) fields.push(`${configured.occurrences} entries`);
    fields.push(configured.enabled ? "enabled" : "disabled");
    meta.textContent = fields.join(" · ");
    details.append(meta);
    appendConfiguredModDependencies(details, configured, configuredByID);

    const link = document.createElement("a");
    link.className = "secondary-link";
    link.textContent = "mod.io";
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    if (metadata && metadata.name_id) {
      link.href = `https://mod.io/g/mordhau/m/${encodeURIComponent(metadata.name_id)}`;
    } else {
      link.href = `https://mod.io/search/mods/${configured.id}`;
    }

    const toggle = makeButton(
      configured.enabled ? "Disable" : "Enable",
      configured.enabled ? "ghost compact" : "primary compact",
      async () => {
        try {
          const result = await api("/api/mods/enabled", {
            method: "POST",
            body: { id: configured.id, enabled: !configured.enabled },
          });
          toast(`${configured.id} ${configured.enabled ? "disabled" : "enabled"}${result.staged ? " in staged Game.ini" : ""}.`);
          await Promise.all([loadMods(), loadConfig()]);
        } catch (error) {
          toast(error.message, true);
        }
      },
    );
    const remove = makeButton("Remove", "danger compact", async () => {
      if (!confirm(`Remove every Mods=${configured.id} entry? Shared dependencies are not removed automatically.`)) return;
      try {
        const result = await api("/api/mods/remove", {
          method: "POST",
          body: { id: configured.id },
        });
        toast(`Mods=${configured.id} removed${result.staged ? " from staged Game.ini" : ""}.`);
        await Promise.all([loadMods(), loadConfig()]);
      } catch (error) {
        toast(error.message, true);
      }
    });
    row.append(details, link, toggle, remove);
    list.append(row);
  }
}

async function loadMods() {
  if (app.modsLoading) {
    app.modsReloadRequested = true;
    return;
  }
  app.modsLoading = true;
  try {
    const data = await api("/api/mods");
    renderConfiguredMods(data);
  } catch (error) {
    toast(error.message, true);
  } finally {
    app.modsLoading = false;
    if (app.modsReloadRequested) {
      app.modsReloadRequested = false;
      loadMods();
    }
  }
}

function renderModPlan(plan) {
  const target = $("#mod-plan");
  target.replaceChildren();
  target.classList.remove("hidden");

  const heading = document.createElement("h3");
  heading.textContent = `${modDisplayName(plan.target)} · Mods=${plan.target.id}`;
  target.append(heading);

  const note = document.createElement("p");
  note.className = "hint";
  if (plan.dependencies_checked) {
    note.textContent = plan.dependencies.length
      ? `${plan.dependencies.length} recursive dependenc${plan.dependencies.length === 1 ? "y" : "ies"} will be added first.`
      : "mod.io reports no dependencies for this mod.";
  } else {
    note.textContent = "Dependencies were not checked because no API key is configured. Only this numeric Resource ID will be added.";
  }
  target.append(note);

  if (plan.dependencies.length) {
    const list = document.createElement("ul");
    list.className = "dependency-list";
    for (const dependency of plan.dependencies) {
      const item = document.createElement("li");
      const name = document.createElement("span");
      name.textContent = modDisplayName(dependency);
      const id = document.createElement("code");
      id.textContent = `Mods=${dependency.id}`;
      item.append(name, id);
      list.append(item);
    }
    target.append(list);
  }
  const add = $("#mod-plan-add");
  add.textContent = plan.dependencies_checked
    ? "Add mod and dependencies to Game.ini"
    : "Add this Resource ID only";
  add.classList.remove("hidden");
}

async function inspectMod(reference) {
  const plan = await api("/api/mods/plan", {
    method: "POST",
    body: { reference },
  });
  if (!plan || !plan.target || !Number.isInteger(plan.target.id)) {
    throw new Error("mod.io returned an invalid install plan.");
  }
  plan.dependencies = Array.isArray(plan.dependencies) ? plan.dependencies : [];
  app.modPlan = plan;
  app.modPlanReference = reference;
  renderModPlan(plan);
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
    username.autocapitalize = "none";
    username.spellcheck = false;
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
    $("#start-map").value = settings.start_map || "";
    const ports = settings.server_ports && typeof settings.server_ports === "object"
      ? settings.server_ports
      : {};
    $("#game-port").value = Number.isInteger(ports.game) ? ports.game : 7777;
    $("#rcon-port").value = Number.isInteger(ports.rcon) ? ports.rcon : 7778;
    $("#beacon-port").value = Number.isInteger(ports.beacon) ? ports.beacon : 15000;
    $("#query-port").value = Number.isInteger(ports.query) ? ports.query : 27015;
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
    row.className = "list-row access-rule-row";
    const action = document.createElement("select");
    action.innerHTML = '<option value="allow">Allow</option><option value="deny">Deny</option>';
    action.value = rule.action;
    action.setAttribute("aria-label", "Rule action");
    const network = document.createElement("input");
    network.value = rule.network;
    network.setAttribute("aria-label", "Network address, CIDR, or IPv4 range");
    network.autocapitalize = "none";
    network.spellcheck = false;
    const comment = document.createElement("input");
    comment.value = typeof rule.comment === "string" ? rule.comment : "";
    comment.placeholder = rule.temporary ? "Automatic emergency rule" : "Comment (optional)";
    comment.setAttribute("aria-label", "Rule comment");
    comment.maxLength = 160;
    comment.autocomplete = "off";
    const save = makeButton("Save", "secondary compact", async () => {
      try {
        await api("/api/access/rule", {
          method: "POST",
          body: {
            id: rule.id,
            action: action.value,
            network: network.value,
            comment: comment.value,
          },
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
      comment.disabled = true;
      save.disabled = true;
      const expiry = rule.expires_at ? new Date(rule.expires_at).toLocaleString() : "soon";
      save.textContent = `Emergency · expires ${expiry}`;
      save.className = "ghost compact temporary-label";
    }
    row.append(action, network, comment, save, remove);
    list.append(row);
  }
}

function bindEvents() {
  $("#theme-toggle").addEventListener("click", () => {
    setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");
  });
  $$(".tab").forEach((tab) => tab.addEventListener("click", () => {
    $$(".tab").forEach((item) => item.classList.toggle("active", item === tab));
    $$(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === `panel-${tab.dataset.panel}`));
  }));
  $$("[data-server-action]").forEach((button) =>
    button.addEventListener("click", () => serverAction(button.dataset.serverAction)));
  $("#rcon-message-form").addEventListener("submit", sendUnicodeMessage);

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
        body: {
          id: "",
          action: $("#new-rule-action").value,
          network: $("#new-rule-network").value,
          comment: $("#new-rule-comment").value,
        },
      });
      $("#new-rule-network").value = "";
      $("#new-rule-comment").value = "";
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
  $("#start-map-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const settings = await api("/api/services/start-map", {
        method: "POST",
        body: { start_map: $("#start-map").value },
      });
      $("#start-map").value = settings.start_map || "";
      toast(settings.start_map
        ? `Initial map saved: ${settings.start_map}`
        : "Initial map cleared; the server default will be used.");
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#server-ports-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/services/server-ports", {
        method: "POST",
        body: {
          game: Number($("#game-port").value),
          rcon: Number($("#rcon-port").value),
          beacon: Number($("#beacon-port").value),
          query: Number($("#query-port").value),
        },
      });
      toast("Dedicated server ports saved. Restart the server to apply them.");
      loadServices();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#modio-settings-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const settings = await api("/api/modio/settings", {
        method: "POST",
        body: {
          api_key: $("#modio-api-key").value,
          api_base: $("#modio-api-base").value,
        },
      });
      $("#modio-api-key").value = "";
      renderModIOSettings(settings);
      toast("mod.io API key validated and saved.");
      await loadMods();
    } catch (error) {
      $("#modio-api-key").value = "";
      toast(error.message, true);
    }
  });
  $("#modio-clear").addEventListener("click", async () => {
    if (!confirm("Clear the saved mod.io API key? Existing Mods= entries are not changed.")) return;
    try {
      await api("/api/modio/settings/clear", { method: "POST" });
      app.modPlan = null;
      app.modPlanReference = "";
      $("#mod-plan").classList.add("hidden");
      $("#mod-plan-add").classList.add("hidden");
      toast("Saved mod.io API key cleared.");
      await loadMods();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#mod-plan-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const reference = $("#mod-reference").value.trim();
    try {
      await inspectMod(reference);
    } catch (error) {
      app.modPlan = null;
      app.modPlanReference = "";
      $("#mod-plan").classList.add("hidden");
      $("#mod-plan-add").classList.add("hidden");
      toast(error.message, true);
    }
  });
  $("#mod-plan-add").addEventListener("click", async () => {
    if (!app.modPlan || !app.modPlanReference) return;
    const count = 1 + app.modPlan.dependencies.length;
    if (!confirm(`Add ${count} Mods= entr${count === 1 ? "y" : "ies"} to Game.ini?`)) return;
    try {
      const result = await api("/api/mods/add", {
        method: "POST",
        body: { reference: app.modPlanReference },
      });
      const changed = result.added.length + result.reenabled.length;
      toast(changed
        ? `${changed} mod entr${changed === 1 ? "y" : "ies"} configured${result.staged ? " in staged Game.ini" : ""}.`
        : "Every mod in this plan is already enabled.");
      app.modPlan = null;
      app.modPlanReference = "";
      $("#mod-plan").classList.add("hidden");
      $("#mod-plan-add").classList.add("hidden");
      await Promise.all([loadMods(), loadConfig()]);
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#mods-refresh").addEventListener("click", async () => {
    const button = $("#mods-refresh");
    button.disabled = true;
    try {
      const data = await api("/api/mods/refresh", { method: "POST" });
      renderConfiguredMods(data);
      if (data.refresh && data.refresh.last_error) {
        toast(`Mod metadata refresh completed with errors: ${data.refresh.last_error}`, true);
      } else {
        toast("Mod metadata refreshed.");
      }
    } catch (error) {
      toast(error.message, true);
    } finally {
      button.disabled = false;
    }
  });
  $("#mods-refresh-minutes").addEventListener("change", async (event) => {
    const minutes = validModRefreshMinutes(event.target.value);
    if (minutes === null) {
      await loadMods();
      toast(`Enter a whole number from ${minimumModRefreshMinutes} to ${maximumModRefreshMinutes} minutes.`, true);
      return;
    }
    try {
      const data = await api("/api/mods/refresh/settings", {
        method: "POST",
        body: { minutes },
      });
      renderConfiguredMods(data);
      toast(`Server-wide mod refresh interval set to ${minutes} minute${minutes === 1 ? "" : "s"}.`);
    } catch (error) {
      await loadMods();
      toast(error.message, true);
    }
  });
}

async function initialize() {
  initializeTheme();
  clearLegacyModRefreshPreference();
  bindEvents();
  try {
    const me = await api("/api/me");
    app.csrf = me.csrf;
    app.username = me.username;
    $("#current-user").textContent = `${me.username} · ${me.current_ip}`;
    const [snapshot, rconHistory] = await Promise.all([
      api("/api/snapshot"),
      api("/api/rcon/history"),
    ]);
    appendRconEvents(rconHistory.events || []);
    renderSnapshot(snapshot);
    await Promise.all([loadConfig(), loadMods(), loadAccounts(), loadAccess(), loadServices()]);
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
