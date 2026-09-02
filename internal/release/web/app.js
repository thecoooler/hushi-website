const metaEl = document.querySelector("#release-meta");
const button = document.querySelector("#download");
const downloadSizeEl = document.querySelector("#download-size");
const versionEl = document.querySelector("#card-version");
const buildEl = document.querySelector("#card-build");
const sizeEl = document.querySelector("#card-size");
const hashEl = document.querySelector("#card-hash");
const statusEl = document.querySelector("#card-status");
const installCommandEl = document.querySelector("#install-command");
const copyInstallButton = document.querySelector("#copy-install");
const copyStatusEl = document.querySelector("#copy-status");

const installCommand = "curl -fsSL https://hushi.icooler.opik.net/install.sh | bash";

function formatBytes(value) {
  if (!Number.isFinite(value) || value <= 0) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1; }
  return `${size >= 10 || unit === 0 ? Math.round(size) : size.toFixed(1)} ${units[unit]}`;
}

async function loadRelease() {
  try {
    const response = await fetch("/api/v1/releases/latest", { cache: "no-store" });
    if (response.status === 404) throw new Error("Релиз ещё не опубликован");
    if (!response.ok) throw new Error("Последний релиз временно недоступен");
    const release = await response.json();
    versionEl.textContent = release.version || "—";
    buildEl.textContent = String(release.version_code || "—");
    const formattedSize = formatBytes(release.size_bytes);
    sizeEl.textContent = formattedSize;
    downloadSizeEl.textContent = formattedSize;
    hashEl.textContent = release.sha256 ? `${release.sha256.slice(0, 12)}…` : "—";
    statusEl.textContent = "READY";
    button.href = release.download_url || "/api/v1/releases/latest/apk";
    button.removeAttribute("aria-disabled");
    metaEl.textContent = `build ${release.version_code} · ${formatBytes(release.size_bytes)} · готов к скачиванию`;
  } catch (error) {
    statusEl.textContent = "EMPTY";
    metaEl.textContent = error.message || "Релиз временно недоступен";
  }
}

loadRelease();

copyInstallButton?.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(installCommand);
    copyInstallButton.textContent = "Команда скопирована";
    copyStatusEl.textContent = "Вставьте её в терминал. Installer спросит подтверждение и настройки автообновления.";
  } catch {
    copyStatusEl.textContent = "Не удалось скопировать автоматически — выделите команду вручную.";
  }
});

if (installCommandEl) installCommandEl.textContent = installCommand;
