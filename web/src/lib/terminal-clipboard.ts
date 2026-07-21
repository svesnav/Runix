// Clipboard bindings shared by every xterm view.
//
// In a terminal, plain Ctrl+C and Ctrl+V are control codes the running
// program needs (SIGINT and ^V), so copy/paste use the Ctrl+Shift variants
// that terminal emulators have long used, plus right-click paste as in
// PuTTY and most Linux terminals.

// Structurally typed so callers need not import xterm at module scope —
// it is loaded dynamically to keep it out of the server bundle.
interface ClipboardTerm {
  getSelection(): string;
  paste(data: string): void;
  attachCustomKeyEventHandler(handler: (e: KeyboardEvent) => boolean): void;
}

// attachClipboard wires the bindings and returns a disposer.
export function attachClipboard(term: ClipboardTerm, holder: HTMLElement): () => void {
  const paste = async () => {
    try {
      const text = await navigator.clipboard.readText();
      if (text) term.paste(text);
    } catch {
      // Clipboard read needs permission and a secure context; without it we
      // fall back to the browser's own paste into xterm's hidden textarea.
    }
  };

  // Returning false stops xterm from also forwarding the keystroke.
  term.attachCustomKeyEventHandler((e) => {
    if (e.type !== "keydown" || !e.ctrlKey || !e.shiftKey) return true;
    const key = e.key.toLowerCase();
    if (key === "c") {
      const sel = term.getSelection();
      if (!sel) return true; // nothing selected — let the process see it
      void navigator.clipboard.writeText(sel);
      return false;
    }
    if (key === "v") {
      void paste();
      return false;
    }
    return true;
  });

  const onContextMenu = (e: MouseEvent) => {
    e.preventDefault();
    void paste();
  };
  holder.addEventListener("contextmenu", onContextMenu);
  return () => holder.removeEventListener("contextmenu", onContextMenu);
}
