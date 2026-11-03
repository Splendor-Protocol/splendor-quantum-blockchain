import { useEffect, useRef, type ReactNode } from 'react';

const FOCUSABLE = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** Open dialogs, innermost last: only that one handles Escape and Tab (a confirm inside Share, say). */
const openDialogs: HTMLElement[] = [];

/**
 * Everything outside `el` becomes inert (no focus, no clicks, hidden from screen readers) while it
 * is open: every sibling of `el` and of each of its ancestors up to <body>. Returns the undo.
 */
function inertOutside(el: HTMLElement): () => void {
  const touched: HTMLElement[] = [];
  for (let node: HTMLElement | null = el; node && node !== document.body; node = node.parentElement) {
    const parent: HTMLElement | null = node.parentElement;
    if (!parent) break;
    for (const sib of Array.from(parent.children)) {
      if (sib === node || !(sib instanceof HTMLElement) || sib.inert || sib.tagName === 'SCRIPT') continue;
      // Toasts stay live so a save confirmation is still heard.
      if (sib.classList.contains('pg-toasts')) continue;
      sib.inert = true;
      touched.push(sib);
    }
  }
  return () => touched.forEach((s) => (s.inert = false));
}

let scrollLocks = 0;
let savedOverflow = '';
function lockScroll(): () => void {
  if (scrollLocks++ === 0) {
    savedOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
  }
  return () => {
    if (--scrollLocks === 0) document.body.style.overflow = savedOverflow;
  };
}

/** Modal dialog: focus moves in on open, Tab stays inside, Escape closes, focus returns on close. */
export function ModalDialog({
  label,
  onClose,
  children,
  className = 'pg-studio-overlay',
  testId,
}: {
  label: string;
  onClose: () => void;
  children: ReactNode;
  className?: string;
  testId?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  close.current = onClose;
  // Read during the first render: a child with autoFocus takes focus before effects run.
  const openerRef = useRef<HTMLElement | null | undefined>(undefined);
  if (openerRef.current === undefined) {
    openerRef.current = typeof document !== 'undefined' && document.activeElement instanceof HTMLElement ? document.activeElement : null;
  }

  useEffect(() => {
    const opener = openerRef.current;
    const self = ref.current;
    if (self) openDialogs.push(self);
    const undoInert = self ? inertOutside(self) : () => undefined;
    const unlock = lockScroll();
    // Respect a child that already took focus with autoFocus.
    if (!ref.current?.contains(document.activeElement)) {
      const first = ref.current?.querySelector<HTMLElement>(FOCUSABLE);
      (first ?? ref.current)?.focus();
    }
    const onKey = (e: globalThis.KeyboardEvent) => {
      const root = ref.current;
      if (!root || openDialogs[openDialogs.length - 1] !== root) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        close.current();
        return;
      }
      if (e.key !== 'Tab') return;
      const items = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null);
      if (!items.length) {
        e.preventDefault();
        return;
      }
      const firstEl = items[0]!;
      const lastEl = items[items.length - 1]!;
      const active = document.activeElement;
      if (!root.contains(active) || active === root) {
        // Focus was lost (e.g. the focused button unmounted between steps): bring it back in.
        e.preventDefault();
        (e.shiftKey ? lastEl : firstEl).focus();
      } else if (e.shiftKey && active === firstEl) {
        e.preventDefault();
        lastEl.focus();
      } else if (!e.shiftKey && active === lastEl) {
        e.preventDefault();
        firstEl.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      const i = self ? openDialogs.lastIndexOf(self) : -1;
      if (i >= 0) openDialogs.splice(i, 1);
      undoInert();
      unlock();
      opener?.focus();
    };
  }, []);

  // A press that starts and ends on the dim backdrop (not on the panel) closes the dialog.
  const downOnBackdrop = useRef(false);
  return (
    <div
      ref={ref}
      className={className}
      role="dialog"
      aria-modal="true"
      aria-label={label}
      tabIndex={-1}
      data-testid={testId}
      onMouseDown={(e) => {
        downOnBackdrop.current = e.target === e.currentTarget;
      }}
      onClick={(e) => {
        if (downOnBackdrop.current && e.target === e.currentTarget) close.current();
        downOnBackdrop.current = false;
      }}
    >
      {children}
    </div>
  );
}
