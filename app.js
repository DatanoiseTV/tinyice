/* tinyice product page */
(() => {
  'use strict';

  const $  = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  const OS_LABEL = {
    linux:    'Linux',
    macos:    'macOS',
    windows:  'Windows',
    freebsd:  'FreeBSD',
    homebrew: 'Homebrew',
    docker:   'Docker',
    source:   'source'
  };

  /* ---------- OS detection ---------- */
  function detectOS() {
    const ua   = navigator.userAgent || '';
    const plat = navigator.platform   || '';
    if (/Win/i.test(plat) || /Windows/i.test(ua))         return 'windows';
    if (/Mac/i.test(plat) || /Mac OS X/i.test(ua))        return 'macos';
    if (/FreeBSD/i.test(ua))                              return 'freebsd';
    if (/Linux/i.test(plat) || /Linux/i.test(ua))         return 'linux';
    return 'linux';
  }

  function defaultArchFor(os) {
    if (os === 'macos') return 'arm64';
    return 'amd64';
  }

  /* ---------- state ---------- */
  const state = { os: 'linux', arch: 'amd64' };

  function archChoicesFor(os) {
    const pres = $$('.dl-code pre[data-os="' + os + '"]');
    const archs = pres
      .map(p => p.dataset.arch)
      .filter(Boolean);
    return [...new Set(archs)];
  }

  function applyState() {
    /* chip + arch button highlight */
    $$('.chip').forEach(c => c.classList.toggle('active', c.dataset.os === state.os));

    const archs = archChoicesFor(state.os);
    const archRow = $('.dl-arch');
    if (archs.length > 1) {
      archRow.hidden = false;
      $$('.arch', archRow).forEach(b => {
        const supported = archs.includes(b.dataset.arch);
        b.disabled = !supported;
        b.style.display = supported ? '' : 'none';
        b.classList.toggle('active', supported && b.dataset.arch === state.arch);
      });
    } else {
      archRow.hidden = true;
    }

    /* show one pre */
    $$('.dl-code pre').forEach(pre => {
      const osOk   = pre.dataset.os === state.os;
      const archOk = !pre.dataset.arch || pre.dataset.arch === state.arch;
      pre.hidden = !(osOk && archOk);
    });

    /* hero CTA label */
    const cta = $('#cta-os');
    if (cta) cta.textContent = OS_LABEL[state.os] || OS_LABEL.linux;

    /* download note swap (small touches per-OS) */
    const note = $('#dl-note');
    if (note) {
      note.innerHTML = downloadNoteFor(state.os);
    }
  }

  function downloadNoteFor(os) {
    switch (os) {
      case 'macos':
        return 'Binaries are not codesigned &mdash; <code>xattr -d com.apple.quarantine</code> clears Gatekeeper. First run writes <code>tinyice.json</code> and prints a generated admin password.';
      case 'windows':
        return 'Run in PowerShell. Windows builds are <code>amd64</code> only. First run writes <code>tinyice.json</code> and prints a generated admin password.';
      case 'homebrew':
        return 'In <a href="https://github.com/Homebrew/homebrew-core/blob/main/Formula/t/tinyice.rb">Homebrew core</a> &mdash; macOS (arm64 + Intel) and Linux (x86_64 + arm64). Pre-built bottles install without compiling. Manage the service with <code>brew services start tinyice</code>; config lives in <code>$(brew --prefix)/var/tinyice/</code>.';
      case 'docker':
        return 'Multi-arch image (linux/amd64, linux/arm64) on GHCR. Tags: <code>:latest</code>, <code>:beta</code>, <code>:vX.Y.Z</code>. The <code>tinyice-data</code> volume keeps config and history across restarts.';
      case 'source':
        return 'Requires Go 1.25+ and Node.js 20+. <code>make build</code> compiles the frontend then the Go binary; all assets are embedded.';
      case 'freebsd':
        return 'Use <code>fetch</code> for the download. First run writes <code>tinyice.json</code> and prints a generated admin password.';
      case 'linux':
      default:
        return 'First run writes <code>tinyice.json</code> and prints a generated admin password. Open <code>http://localhost:8000</code>. Bind to 80/443 without root via <code>sudo setcap \'cap_net_bind_service=+ep\' ./tinyice</code>.';
    }
  }

  function setOS(os) {
    state.os = os;
    const archs = archChoicesFor(os);
    if (archs.length > 0) {
      const preferred = defaultArchFor(os);
      state.arch = archs.includes(preferred) ? preferred : archs[0];
    }
    applyState();
  }

  function setArch(arch) {
    if (!archChoicesFor(state.os).includes(arch)) return;
    state.arch = arch;
    applyState();
  }

  /* ---------- copy ---------- */
  function visibleCommand() {
    const pre = $('.dl-code pre:not([hidden])');
    return pre ? pre.textContent : '';
  }

  async function copyToClipboard(text) {
    if (navigator.clipboard && window.isSecureContext) {
      try { await navigator.clipboard.writeText(text); return true; } catch (_) {}
    }
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '-1000px';
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (_) {}
    document.body.removeChild(ta);
    return ok;
  }

  function initCopy() {
    const btn = $('.copy');
    if (!btn) return;
    btn.addEventListener('click', async () => {
      const ok = await copyToClipboard(visibleCommand().trim());
      const orig = btn.textContent;
      btn.textContent = ok ? 'Copied' : 'Failed';
      btn.classList.toggle('copied', ok);
      setTimeout(() => {
        btn.textContent = orig;
        btn.classList.remove('copied');
      }, 1400);
    });
  }

  /* ---------- chip/arch wiring ---------- */
  function initChips() {
    $$('.chip').forEach(c => c.addEventListener('click', () => setOS(c.dataset.os)));
    $$('.arch').forEach(a => a.addEventListener('click', () => setArch(a.dataset.arch)));
  }

  /* ---------- latest version (cached per session) ---------- */
  async function loadLatestVersion() {
    const targets = $$('[data-version-target]');
    if (targets.length === 0) return;

    const KEY = 'tinyice:latest-tag';
    const TTL = 6 * 60 * 60 * 1000;
    let cached = null;
    try {
      const raw = sessionStorage.getItem(KEY);
      if (raw) {
        const o = JSON.parse(raw);
        if (o && o.tag && o.at && (Date.now() - o.at) < TTL) cached = o.tag;
      }
    } catch (_) {}

    if (cached) {
      targets.forEach(el => el.textContent = cached);
      return;
    }

    try {
      const res = await fetch(
        'https://api.github.com/repos/DatanoiseTV/tinyice/releases/latest',
        { headers: { 'Accept': 'application/vnd.github+json' } }
      );
      if (!res.ok) return;
      const data = await res.json();
      const tag = data && data.tag_name;
      if (!tag) return;
      targets.forEach(el => el.textContent = tag);
      try { sessionStorage.setItem(KEY, JSON.stringify({ tag, at: Date.now() })); } catch (_) {}
    } catch (_) { /* silent */ }
  }

  /* ---------- boot ---------- */
  function ready(fn) {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', fn, { once: true });
    } else {
      fn();
    }
  }

  ready(() => {
    initChips();
    initCopy();
    setOS(detectOS());
    loadLatestVersion();
  });
})();
