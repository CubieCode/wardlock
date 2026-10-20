// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Opens the collapsed <details> blocks around the target of a #fragment, so
// links and the table of contents reach headings inside folded sections in
// browsers that do not reveal them on their own. The pages work without it.
function reveal() {
  const id = decodeURIComponent(location.hash.slice(1));
  const target = id && document.getElementById(id);
  if (!target) return;
  for (let el = target.parentElement; el; el = el.parentElement)
    if (el instanceof HTMLDetailsElement) el.open = true;
  target.scrollIntoView();
}
addEventListener('hashchange', reveal);
reveal();
