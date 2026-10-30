// The Runova mark: a static template, no data in it.
const MARK = `<svg class="rmark" viewBox="0 0 256 256" aria-hidden="true"><g transform="translate(33.28 33.28) scale(0.74)"><g transform="translate(140.25 111.75) rotate(45) scale(1.02) translate(-128 -124)"><path class="rb" fill-rule="evenodd" d="M128 20 C152 36 160 60 160 92 V176 H96 V92 C96 60 104 36 128 20 Z M115 96 H141 A7 7 0 0 1 141 110 H115 A7 7 0 0 1 115 96 Z M115 124 H141 A7 7 0 0 1 141 138 H115 A7 7 0 0 1 115 124 Z"/><path class="rb" d="M96 136 L64 168 V200 L96 184 Z"/><path class="rb" d="M160 136 L192 168 V200 L160 184 Z"/><path class="rf" d="M108 188 H148 L128 228 Z"/></g></g></svg>`;

export function logo(cls = "logo") {
  const el = document.createElement("span");
  el.className = cls;
  el.innerHTML = MARK;
  return el;
}
