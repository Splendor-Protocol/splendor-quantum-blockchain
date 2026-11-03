import { useMemo, useState } from 'react';
import { ModalDialog } from '../components/ModalDialog';

export type Command = {
  id: string;
  label: string;
  /** Group shown on the right ("Add", "Page", "Panel", "Project"). */
  group: string;
  /** Extra words that should match ("component", the category, the import name). */
  keywords?: string;
  run: () => void;
};

/** Case-insensitive match of every word of `query` against a command's label, group and keywords. */
export function matchCommand(c: Command, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const hay = `${c.label} ${c.group} ${c.keywords ?? ''}`.toLowerCase();
  return words.every((w) => hay.includes(w));
}

const MAX_SHOWN = 40;

/** Ctrl/Cmd+K: type to find a command (add a component, open a page or panel, export…), Enter runs it. */
export function CommandPalette({ commands, onClose }: { commands: readonly Command[]; onClose: () => void }) {
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const shown = useMemo(() => commands.filter((c) => matchCommand(c, query)).slice(0, MAX_SHOWN), [commands, query]);
  const current = Math.min(active, Math.max(0, shown.length - 1));
  const run = (c: Command | undefined) => {
    if (!c) return;
    onClose();
    c.run();
  };
  return (
    <ModalDialog label="Command palette" onClose={onClose} className="pg-modal pg-command-palette" testId="command-palette">
      <div className="pg-modal-panel">
        <input
          autoFocus
          className="pg-command-input"
          role="combobox"
          aria-expanded="true"
          aria-controls="pg-command-list"
          aria-activedescendant={shown[current] ? `pg-cmd-${shown[current]!.id}` : undefined}
          aria-label="Type a command or a component name"
          placeholder="Add a component, go to a page, export…"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') {
              e.preventDefault();
              setActive((i) => Math.min(i + 1, shown.length - 1));
            } else if (e.key === 'ArrowUp') {
              e.preventDefault();
              setActive((i) => Math.max(i - 1, 0));
            } else if (e.key === 'Enter') {
              e.preventDefault();
              run(shown[current]);
            }
          }}
        />
        <ul id="pg-command-list" role="listbox" className="pg-command-list" aria-label="Commands">
          {shown.map((c, i) => (
            <li
              key={c.id}
              id={`pg-cmd-${c.id}`}
              role="option"
              aria-selected={i === current}
              className={i === current ? 'is-active' : undefined}
              onMouseEnter={() => setActive(i)}
              onClick={() => run(c)}
            >
              <span>{c.label}</span>
              <small className="pg-muted">{c.group}</small>
            </li>
          ))}
          {shown.length === 0 ? <li className="pg-muted pg-command-empty">No command matches “{query}”.</li> : null}
        </ul>
        <p className="pg-muted pg-command-hint">
          <kbd>↑</kbd> <kbd>↓</kbd> to move · <kbd>Enter</kbd> to run · <kbd>Esc</kbd> to close
        </p>
      </div>
    </ModalDialog>
  );
}

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);

/** "Ctrl" or "⌘" for the shortcut labels on this platform. */
export function modKey(): string {
  return isMac() ? '⌘' : 'Ctrl';
}

/** `?`: every studio shortcut, with the right modifier for this platform. */
export function ShortcutSheet({ onClose }: { onClose: () => void }) {
  const mod = modKey();
  const rows: [string, string][] = [
    [`${mod} K`, 'Command palette'],
    [`${mod} Z`, 'Undo'],
    [`${mod} Shift Z / ${mod} Y`, 'Redo'],
    [`${mod} C / ${mod} V`, 'Copy / paste the selection'],
    [`${mod} D`, 'Duplicate the selection'],
    ['Delete / Backspace', 'Delete the selection'],
    ['Alt ↑ / Alt ↓', 'Move a layer up or down (Layers)'],
    ['← / →', 'Collapse / expand a box (Layers)'],
    ['F2', 'Rename a box (Layers)'],
    ['Esc', 'Close a drawer or dialog'],
    ['?', 'This list'],
  ];
  return (
    <ModalDialog label="Keyboard shortcuts" onClose={onClose} className="pg-modal" testId="shortcut-sheet">
      <div className="pg-modal-panel">
        <h2 style={{ marginTop: 0 }}>Keyboard shortcuts</h2>
        <table className="pg-shortcuts">
          <tbody>
            {rows.map(([keys, what]) => (
              <tr key={what}>
                <th scope="row">
                  <kbd>{keys}</kbd>
                </th>
                <td>{what}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="pg-row" style={{ justifyContent: 'flex-end' }}>
          <button type="button" className="pg-btn" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </ModalDialog>
  );
}
