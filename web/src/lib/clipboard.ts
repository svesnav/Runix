// Clipboard access that survives being served over plain HTTP.
//
// `navigator.clipboard` only exists in a secure context. Runix is very often
// reached at http://<lan-ip>:8080, which is not one, so the whole API is
// simply undefined there and every copy silently did nothing. Writing has a
// working fallback (a hidden textarea plus the old execCommand); reading
// does not, which the UI has to say out loud rather than pretend.

export function canWriteClipboard(): boolean {
  return true; // there is always the textarea fallback
}

// canReadClipboard reports whether we can pull text out of the clipboard
// ourselves. When false the browser's own paste (Ctrl+V) still works,
// because that delivers a paste event we never have to ask for.
export function canReadClipboard(): boolean {
  return typeof navigator !== "undefined" && Boolean(navigator.clipboard?.readText);
}

export async function copyText(text: string): Promise<boolean> {
  if (!text) return false;
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Permission denied or a non-secure context: fall through.
    }
  }
  return legacyCopy(text);
}

export async function readText(): Promise<string | null> {
  if (!navigator.clipboard?.readText) return null;
  try {
    return await navigator.clipboard.readText();
  } catch {
    return null;
  }
}

// legacyCopy is deprecated everywhere and still the only thing that works
// outside a secure context. The textarea must be visible enough to focus,
// hence the off-screen position rather than display:none.
function legacyCopy(text: string): boolean {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.top = "-1000px";
  area.style.opacity = "0";
  document.body.appendChild(area);
  try {
    area.select();
    area.setSelectionRange(0, text.length);
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    document.body.removeChild(area);
  }
}
