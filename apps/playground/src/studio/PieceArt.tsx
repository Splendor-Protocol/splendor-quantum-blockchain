import { useId, type ReactNode } from 'react';

/** Accent per palette shelf (`groupMeta(...).tone`). */
export const TONE_COLORS: Record<string, string> = {
  violet: '#8b6cff',
  mint: '#3ecf8e',
  amber: '#f0c14a',
  sky: '#5cb8ff',
  rose: '#ff7a8a',
  pink: '#e98bd8',
  slate: '#9aa4b2',
};

const INK = '#e8ecf3';
const MUTED = '#4d5566';
const CARD = '#1d2230';
const LINE = '#2c3242';
const FIELD = '#0f121a';

// Primitives on a 120 × 76 canvas.
const text = (x: number, y: number, w: number, h = 3, o = 0.9) => <rect x={x} y={y} width={w} height={h} rx={h / 2} fill={INK} opacity={o} />;
const muted = (x: number, y: number, w: number, h = 2.5) => <rect x={x} y={y} width={w} height={h} rx={h / 2} fill={MUTED} />;
const card = (x: number, y: number, w: number, h: number, r = 4) => <rect x={x} y={y} width={w} height={h} rx={r} fill={CARD} stroke={LINE} strokeWidth={0.8} />;
const field = (x: number, y: number, w: number, h = 8) => <rect x={x} y={y} width={w} height={h} rx={2.5} fill={FIELD} stroke={LINE} strokeWidth={0.8} />;
const btn = (a: string, x: number, y: number, w: number, h = 8, ghost = false) =>
  ghost ? <rect x={x} y={y} width={w} height={h} rx={h / 2} fill="none" stroke={a} strokeWidth={1} /> : <rect x={x} y={y} width={w} height={h} rx={h / 2} fill={a} />;
const dot = (a: string, cx: number, cy: number, r: number, o = 1) => <circle cx={cx} cy={cy} r={r} fill={a} opacity={o} />;
const ring = (a: string, cx: number, cy: number, r: number) => <circle cx={cx} cy={cy} r={r} fill="none" stroke={a} strokeWidth={1.2} />;

/** uid is unique per rendered PieceArt, for ids that must not collide across instances (gradients). */
type Art = (a: string, uid: string) => ReactNode;

const ART: Record<string, Art> = {
  // ---- Page / layout --------------------------------------------------------------------
  hero: (a) => (
    <>
      {text(14, 20, 62, 6)}
      {muted(14, 32, 86)}
      {muted(14, 38, 70)}
      {btn(a, 14, 48, 28)}
    </>
  ),
  'hero-centered': (a) => (
    <>
      {text(28, 16, 64, 6)}
      {muted(20, 28, 80)}
      {muted(30, 34, 60)}
      {btn(a, 38, 46, 44, 9)}
    </>
  ),
  'hero-split': (a) => (
    <>
      {text(10, 18, 46, 5)}
      {muted(10, 28, 48)}
      {muted(10, 34, 40)}
      {btn(a, 10, 44, 26)}
      <rect x={66} y={12} width={44} height={52} rx={5} fill={a} opacity={0.25} />
      <path d="M72 56 L84 40 L92 50 L98 44 L106 56 Z" fill={a} opacity={0.8} />
      {dot(a, 98, 24, 4)}
    </>
  ),
  'hero-gradient': (a, uid) => (
    <>
      <defs>
        <linearGradient id={`${uid}-grad`} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor={a} stopOpacity={0.9} />
          <stop offset="1" stopColor="#ff7ac6" stopOpacity={0.6} />
        </linearGradient>
      </defs>
      <rect x={6} y={8} width={108} height={60} rx={6} fill={`url(#${uid}-grad)`} />
      <rect x={28} y={22} width={64} height={6} rx={3} fill="#fff" />
      <rect x={34} y={33} width={52} height={3} rx={1.5} fill="#fff" opacity={0.7} />
      <rect x={42} y={45} width={36} height={9} rx={4.5} fill="#fff" />
    </>
  ),
  heading: () => (
    <>
      <text x={14} y={44} fontSize={26} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        Aa
      </text>
      {text(56, 28, 48, 5)}
      {muted(56, 40, 38)}
    </>
  ),
  'rich-text': () => (
    <>
      {text(14, 16, 50, 4)}
      {muted(14, 26, 92)}
      {muted(14, 32, 88)}
      {muted(14, 38, 92)}
      {muted(14, 44, 76)}
      {muted(14, 50, 84)}
    </>
  ),
  'action-button': (a) => (
    <>
      {btn(a, 26, 28, 68, 16)}
      <rect x={42} y={34.5} width={36} height={3} rx={1.5} fill="#fff" />
      <path d="M86 50 l6 12 l2 -5 l5 -2 Z" fill={INK} />
    </>
  ),
  'feature-grid': (a) => (
    <>
      {text(36, 10, 48, 4)}
      {[10, 46, 82].map((x) => (
        <g key={x}>
          {card(x, 22, 30, 44)}
          <rect x={x + 5} y={27} width={9} height={9} rx={2.5} fill={a} />
          {text(x + 5, 41, 18, 2.5)}
          {muted(x + 5, 47, 20, 2)}
          {muted(x + 5, 52, 16, 2)}
        </g>
      ))}
    </>
  ),
  faq: (a) => (
    <>
      {[12, 30, 48].map((y, i) => (
        <g key={y}>
          {card(10, y, 100, 14, 3)}
          {text(16, y + 5.5, i === 0 ? 50 : 42, 2.8)}
          <path d={i === 0 ? `M100 ${y + 5} l3 3 l3 -3` : `M101 ${y + 4} l3 3 l-3 3`} stroke={a} strokeWidth={1.4} fill="none" strokeLinecap="round" />
        </g>
      ))}
    </>
  ),
  'cta-banner': (a) => (
    <>
      <rect x={8} y={18} width={104} height={40} rx={6} fill={a} opacity={0.22} stroke={a} strokeWidth={0.8} />
      {text(18, 28, 46, 5)}
      {muted(18, 38, 52)}
      {btn(a, 78, 31, 26, 10)}
    </>
  ),
  'footer-bar': (a) => (
    <>
      {muted(10, 16, 100, 2)}
      {muted(10, 22, 80, 2)}
      <rect x={6} y={50} width={108} height={20} rx={3} fill={CARD} />
      {dot(a, 18, 60, 3)}
      {muted(26, 59, 40)}
      {muted(84, 59, 24)}
    </>
  ),
  'stat-card': (a) => (
    <>
      {card(18, 12, 84, 52, 6)}
      {muted(26, 20, 30)}
      <text x={26} y={46} fontSize={20} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        1.2k
      </text>
      <path d="M72 50 L80 42 L86 46 L94 32" stroke={a} strokeWidth={2} fill="none" strokeLinecap="round" />
      {muted(26, 54, 24, 2)}
    </>
  ),
  spacer: (a) => (
    <>
      {card(14, 8, 92, 14, 3)}
      {card(14, 54, 92, 14, 3)}
      <path d="M60 26 v24 M55 31 l5 -5 l5 5 M55 45 l5 5 l5 -5" stroke={a} strokeWidth={1.5} fill="none" strokeLinecap="round" />
    </>
  ),
  divider: (a) => (
    <>
      {muted(14, 18, 80)}
      <line x1={10} y1={38} x2={46} y2={38} stroke={LINE} strokeWidth={1.5} />
      <line x1={74} y1={38} x2={110} y2={38} stroke={LINE} strokeWidth={1.5} />
      {dot(a, 60, 38, 4)}
      {muted(14, 54, 72)}
    </>
  ),
  'image-block': (a) => (
    <>
      <rect x={14} y={8} width={92} height={50} rx={5} fill={a} opacity={0.2} />
      <path d="M20 52 L44 28 L60 44 L72 34 L100 52 Z" fill={a} opacity={0.85} />
      {dot('#fff', 88, 20, 5, 0.85)}
      {muted(34, 64, 52)}
    </>
  ),
  section: (a) => (
    <>
      <rect x={6} y={8} width={108} height={60} rx={5} fill="none" stroke={a} strokeWidth={1.3} strokeDasharray="4 3" />
      {text(16, 18, 40, 4)}
      {card(16, 28, 88, 32, 3)}
    </>
  ),
  row: (a) => (
    <>
      <rect x={6} y={18} width={108} height={40} rx={5} fill="none" stroke={a} strokeWidth={1.3} strokeDasharray="4 3" />
      {[12, 46, 80].map((x) => card(x, 24, 28, 28, 3))}
      <path d="M42 38 h2 M76 38 h2" stroke={a} strokeWidth={1.5} />
    </>
  ),
  columns: (a) => (
    <>
      {[8, 46, 84].map((x) => (
        <g key={x}>
          <rect x={x} y={8} width={28} height={60} rx={4} fill={a} opacity={0.14} stroke={a} strokeWidth={0.8} />
          {text(x + 5, 16, 16, 2.5)}
          {muted(x + 5, 23, 18, 2)}
          {muted(x + 5, 28, 14, 2)}
        </g>
      ))}
    </>
  ),
  card: (a) => (
    <>
      <rect x={20} y={10} width={80} height={56} rx={7} fill={CARD} stroke={a} strokeWidth={1} />
      <rect x={20} y={10} width={80} height={20} rx={7} fill={a} opacity={0.3} />
      {text(28, 36, 40, 3.5)}
      {muted(28, 45, 60)}
      {muted(28, 51, 48)}
    </>
  ),
  badge: (a) => (
    <>
      <rect x={30} y={22} width={60} height={14} rx={7} fill={a} opacity={0.25} stroke={a} strokeWidth={0.8} />
      {dot(a, 39, 29, 2.5)}
      <rect x={45} y={27.5} width={36} height={3} rx={1.5} fill={a} />
      {text(28, 46, 64, 4, 0.6)}
    </>
  ),
  steps: (a) => (
    <>
      <line x1={22} y1={26} x2={98} y2={26} stroke={LINE} strokeWidth={2} />
      <line x1={22} y1={26} x2={60} y2={26} stroke={a} strokeWidth={2} />
      {[22, 60, 98].map((cx, i) => (
        <g key={cx}>
          {i < 2 ? dot(a, cx, 26, 7) : <circle cx={cx} cy={26} r={7} fill={CARD} stroke={LINE} strokeWidth={1.2} />}
          <text x={cx} y={29} fontSize={8} fontWeight={800} textAnchor="middle" fill={i < 2 ? '#fff' : MUTED} fontFamily="system-ui, sans-serif">
            {i + 1}
          </text>
          {text(cx - 12, 42, 24, 2.5, i < 2 ? 0.9 : 0.4)}
          {muted(cx - 10, 49, 20, 2)}
        </g>
      ))}
    </>
  ),
  'pricing-card': (a) => (
    <>
      <rect x={30} y={6} width={60} height={64} rx={7} fill={CARD} stroke={a} strokeWidth={1.2} />
      {muted(40, 14, 26)}
      <text x={40} y={34} fontSize={15} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        $9
      </text>
      {[40, 46, 52].map((y) => (
        <g key={y}>
          {dot(a, 42, y + 1, 1.6)}
          {muted(47, y, 30, 2)}
        </g>
      ))}
      {btn(a, 38, 58, 44, 7)}
    </>
  ),
  testimonial: (a) => (
    <>
      {card(10, 10, 100, 56, 6)}
      <text x={16} y={30} fontSize={22} fontWeight={800} fill={a} fontFamily="Georgia, serif">
        “
      </text>
      {muted(32, 20, 70)}
      {muted(32, 26, 66)}
      {muted(32, 32, 50)}
      {dot(a, 24, 52, 6, 0.8)}
      {text(34, 48, 32, 3)}
      {muted(34, 55, 22, 2)}
    </>
  ),
  'logo-row': (a) => (
    <>
      {muted(36, 16, 48)}
      {[14, 38, 62, 86].map((x, i) => (
        <g key={x} opacity={0.55 + i * 0.1}>
          {i % 2 ? <rect x={x} y={32} width={20} height={14} rx={3} fill={a} /> : <circle cx={x + 10} cy={39} r={7} fill={a} />}
        </g>
      ))}
      {muted(14, 58, 92, 1.5)}
    </>
  ),
  navbar: (a) => (
    <>
      <rect x={4} y={10} width={112} height={18} rx={4} fill={CARD} stroke={LINE} strokeWidth={0.8} />
      {dot(a, 14, 19, 4)}
      {text(21, 17.5, 16, 3)}
      {muted(50, 18, 10)}
      {muted(64, 18, 10)}
      {muted(78, 18, 8)}
      {btn(a, 92, 14, 20, 10)}
      {muted(10, 40, 60)}
      {muted(10, 46, 80)}
    </>
  ),

  // ---- Wallet ---------------------------------------------------------------------------
  'account-card': (a, uid) => (
    <>
      {card(10, 8, 100, 60, 7)}
      <defs>
        <linearGradient id={`${uid}-av`} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor={a} />
          <stop offset="1" stopColor="#5cb8ff" />
        </linearGradient>
      </defs>
      <circle cx={26} cy={26} r={9} fill={`url(#${uid}-av)`} />
      {text(40, 21, 36, 3)}
      <rect x={40} y={28} width={44} height={3} rx={1.5} fill={MUTED} />
      <text x={18} y={52} fontSize={13} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        899.9
      </text>
      <rect x={60} y={45} width={20} height={7} rx={3.5} fill={a} opacity={0.3} />
      {btn(a, 86, 54, 18, 7, true)}
    </>
  ),
  transfer: (a) => (
    <>
      {card(10, 4, 100, 68, 7)}
      {muted(18, 11, 20, 2)}
      {field(18, 15, 84, 9)}
      {muted(22, 18.5, 46)}
      {muted(18, 29, 16, 2)}
      {field(18, 33, 84, 9)}
      <text x={22} y={40} fontSize={7} fontWeight={700} fill={INK} fontFamily="system-ui, sans-serif">
        0.01
      </text>
      <text x={84} y={40} fontSize={6} fill={MUTED} fontFamily="system-ui, sans-serif">
        SPLD
      </text>
      {btn(a, 18, 52, 84, 11)}
      <path d="M56 57.5 h8 m-3 -3 l3 3 l-3 3" stroke="#fff" strokeWidth={1.3} fill="none" strokeLinecap="round" />
    </>
  ),
  'balance-summary': (a) => (
    <>
      {card(10, 8, 100, 60, 7)}
      {muted(18, 16, 30)}
      <text x={18} y={38} fontSize={16} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        899.99
      </text>
      <text x={80} y={38} fontSize={7} fill={MUTED} fontFamily="system-ui, sans-serif">
        SPLD
      </text>
      <rect x={18} y={46} width={30} height={7} rx={3.5} fill={a} opacity={0.3} />
      {muted(54, 48, 30)}
      {ring(a, 98, 18, 5)}
      <path d="M98 14.5 a3.5 3.5 0 1 1 -3.4 2.6" stroke={a} strokeWidth={1.2} fill="none" />
    </>
  ),
  'tip-jar': (a) => (
    <>
      <path d="M44 20 h32 l-3 40 a4 4 0 0 1 -4 4 h-18 a4 4 0 0 1 -4 -4 Z" fill={a} opacity={0.2} stroke={a} strokeWidth={1.2} />
      <rect x={42} y={14} width={36} height={7} rx={2} fill={a} opacity={0.6} />
      {dot('#f0c14a', 54, 48, 5)}
      {dot('#f0c14a', 64, 42, 5, 0.85)}
      {dot('#f0c14a', 58, 34, 4, 0.7)}
      {btn(a, 84, 50, 26, 9)}
      {muted(84, 40, 22)}
    </>
  ),

  // ---- Tokens ---------------------------------------------------------------------------
  'token-panel': (a) => (
    <>
      {card(10, 6, 100, 64, 7)}
      {dot(a, 24, 20, 7)}
      <text x={24} y={23} fontSize={8} fontWeight={800} textAnchor="middle" fill="#1a1a1a" fontFamily="system-ui, sans-serif">
        Q
      </text>
      {text(36, 15, 30, 3)}
      {muted(36, 22, 22)}
      <text x={74} y={24} fontSize={9} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        1,000
      </text>
      {field(18, 34, 84, 9)}
      {btn(a, 18, 50, 40, 10)}
      {btn(a, 62, 50, 40, 10, true)}
    </>
  ),
  'deploy-token': (a) => (
    <>
      {card(10, 4, 100, 68, 7)}
      {field(18, 11, 40, 8)}
      {field(62, 11, 40, 8)}
      {field(18, 24, 84, 8)}
      {muted(22, 27, 30)}
      {field(18, 37, 40, 8)}
      {btn(a, 18, 54, 84, 11)}
      <path d="M57 63 l3 -6 l3 6 M60 57 v-2" stroke="#fff" strokeWidth={1.3} fill="none" strokeLinecap="round" />
      {dot(a, 82, 41, 3)}
    </>
  ),
  'token-info': (a) => (
    <>
      {card(10, 10, 100, 56, 7)}
      <circle cx={32} cy={38} r={14} fill={a} />
      <text x={32} y={42.5} fontSize={12} fontWeight={800} textAnchor="middle" fill="#1a1a1a" fontFamily="system-ui, sans-serif">
        T
      </text>
      {text(54, 24, 40, 4)}
      {muted(54, 34, 24)}
      {muted(54, 42, 44)}
      {muted(54, 50, 30)}
    </>
  ),
  'token-stats': (a) => (
    <>
      {card(8, 10, 104, 56, 6)}
      {dot(a, 22, 24, 6)}
      {text(32, 21, 30, 3.5)}
      {muted(32, 28, 20, 2)}
      {[16, 46, 78].map((x, i) => (
        <g key={x}>
          {muted(x, 42, 18, 2)}
          <rect x={x} y={48} width={i === 1 ? 26 : 20} height={5} rx={2.5} fill={i === 0 ? a : INK} opacity={i === 0 ? 0.9 : 0.75} />
        </g>
      ))}
    </>
  ),
  tokenomics: (a) => {
    // Donut: 40 / 25 / 20 / 15 % of a ring with radius 20.
    const C = 2 * Math.PI * 20;
    const slices: [number, string][] = [
      [0.4, a],
      [0.25, '#3ecf8e'],
      [0.2, '#f0c14a'],
      [0.15, '#5cb8ff'],
    ];
    let offset = 0;
    return (
      <>
        <circle cx={38} cy={38} r={20} fill="none" stroke={LINE} strokeWidth={10} />
        <g transform="rotate(-90 38 38)">
          {slices.map(([share, color]) => {
            const el = <circle key={color} cx={38} cy={38} r={20} fill="none" stroke={color} strokeWidth={10} strokeDasharray={`${share * C - 1} ${C - share * C + 1}`} strokeDashoffset={-offset} />;
            offset += share * C;
            return el;
          })}
        </g>
        {slices.map(([, color], i) => (
          <g key={color}>
            <rect x={72} y={18 + i * 12} width={7} height={7} rx={2} fill={color} />
            {text(84, 19.5 + i * 12, 22 - i * 3, 3, 0.8)}
          </g>
        ))}
      </>
    );
  },

  // ---- Sign-in --------------------------------------------------------------------------
  'sign-in': (a) => (
    <>
      {btn(a, 22, 26, 76, 22)}
      <rect x={32} y={32} width={10} height={10} rx={2} fill="#fff" opacity={0.9} />
      <path d="M35 37 l2 2 l3 -4" stroke={a} strokeWidth={1.5} fill="none" strokeLinecap="round" />
      <rect x={48} y={35.5} width={40} height={3.5} rx={1.75} fill="#fff" />
      {muted(34, 58, 52)}
    </>
  ),
  'members-area': (a) => (
    <>
      {card(10, 8, 100, 60, 7)}
      <rect x={50} y={28} width={20} height={16} rx={3} fill={a} />
      <path d="M54 28 v-5 a6 6 0 0 1 12 0 v5" stroke={a} strokeWidth={2.2} fill="none" />
      {dot('#1a1a1a', 60, 35, 2)}
      {muted(30, 52, 60)}
      {muted(40, 58, 40, 2)}
    </>
  ),

  // ---- Activity -------------------------------------------------------------------------
  'pending-tx': (a) => (
    <>
      {card(10, 6, 100, 64, 7)}
      {[16, 32, 48].map((y, i) => (
        <g key={y}>
          {i === 0 ? ring(a, 22, y + 5, 4) : dot(i === 1 ? '#3ecf8e' : MUTED, 22, y + 5, 4)}
          {i === 0 ? <path d={`M22 ${y + 1} a4 4 0 0 1 4 4`} stroke={a} strokeWidth={1.6} fill="none" /> : null}
          {text(32, y + 2, 38, 2.8)}
          {muted(32, y + 7, 26, 2)}
          {muted(84, y + 3.5, 18)}
        </g>
      ))}
    </>
  ),
  'tx-status': (a) => (
    <>
      {card(14, 8, 92, 60, 7)}
      <circle cx={60} cy={30} r={12} fill={a} opacity={0.25} />
      <path d="M54 30 l4 4 l8 -8" stroke={a} strokeWidth={2.4} fill="none" strokeLinecap="round" strokeLinejoin="round" />
      {text(40, 48, 40, 3.5)}
      {muted(34, 56, 52, 2)}
    </>
  ),
  'tx-history': (a) => (
    <>
      {card(6, 6, 108, 64, 5)}
      <rect x={6} y={6} width={108} height={11} rx={5} fill={a} opacity={0.2} />
      {[12, 46, 82].map((x) => muted(x, 10.5, 18, 2))}
      {[22, 33, 44, 55].map((y, i) => (
        <g key={y}>
          {dot(i % 2 ? '#3ecf8e' : a, 15, y + 2, 2)}
          {text(21, y + 0.5, 20, 2.5, 0.75)}
          {muted(46, y + 0.5, 26, 2.5)}
          {muted(82, y + 0.5, 20, 2.5)}
        </g>
      ))}
    </>
  ),

  // ---- Collectibles ---------------------------------------------------------------------
  'nft-drop': (a) => (
    <>
      <rect x={14} y={8} width={46} height={60} rx={6} fill={a} opacity={0.3} />
      <path d="M22 54 L32 38 L40 48 L46 42 L54 54 Z" fill={a} />
      {dot('#fff', 46, 22, 4, 0.9)}
      {text(68, 16, 36, 4)}
      {muted(68, 26, 38)}
      {muted(68, 32, 30)}
      {field(68, 42, 38, 8)}
      {btn(a, 68, 56, 38, 10)}
    </>
  ),
  'nft-gallery': (a) => (
    <>
      {[
        [8, 8],
        [46, 8],
        [84, 8],
        [8, 42],
        [46, 42],
        [84, 42],
      ].map(([x, y], i) => (
        <g key={i}>
          <rect x={x} y={y} width={28} height={28} rx={4} fill={a} opacity={0.18 + (i % 3) * 0.14} />
          <path d={`M${x! + 4} ${y! + 23} L${x! + 11} ${y! + 13} L${x! + 17} ${y! + 19} L${x! + 24} ${y! + 23} Z`} fill={a} />
        </g>
      ))}
    </>
  ),

  // ---- Contracts ------------------------------------------------------------------------
  'contract-call': (a) => (
    <>
      {card(10, 6, 100, 64, 6)}
      <text x={18} y={22} fontSize={9} fontWeight={700} fill={a} fontFamily="ui-monospace, monospace">
        {'fn()'}
      </text>
      {field(18, 28, 84, 9)}
      {btn(a, 18, 44, 40, 10)}
      {btn(a, 62, 44, 40, 10, true)}
      {muted(18, 60, 60)}
    </>
  ),
  'contract-read': (a) => (
    <>
      {card(10, 8, 100, 60, 6)}
      <text x={18} y={22} fontSize={8} fontWeight={700} fill={a} fontFamily="ui-monospace, monospace">
        get()
      </text>
      <text x={18} y={48} fontSize={20} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        42
      </text>
      <path d="M92 16 a6 6 0 1 1 -5 2.5" stroke={a} strokeWidth={1.4} fill="none" strokeLinecap="round" />
      <path d="M85 15 l2 3.5 l3.5 -1.5" stroke={a} strokeWidth={1.4} fill="none" strokeLinecap="round" />
      {muted(18, 56, 40, 2)}
    </>
  ),
  'contract-write': (a) => (
    <>
      {card(10, 6, 100, 64, 6)}
      <text x={18} y={20} fontSize={8} fontWeight={700} fill={a} fontFamily="ui-monospace, monospace">
        set(uint256)
      </text>
      {field(18, 26, 84, 9)}
      <text x={22} y={33} fontSize={6.5} fill={INK} fontFamily="ui-monospace, monospace">
        42
      </text>
      {btn(a, 18, 46, 84, 12)}
      <path d="M54 52 l4 -4 l2 2 l-4 4 h-2 Z M62 46 l2 2" stroke="#fff" strokeWidth={1.2} fill="none" strokeLinejoin="round" />
    </>
  ),
  'contract-action': (a) => (
    <>
      {btn(a, 24, 22, 72, 20)}
      <path d="M52 27 l-5 7 h5 l-2 7 l8 -9 h-5 l2 -5 Z" fill="#fff" />
      <rect x={62} y={30.5} width={22} height={3} rx={1.5} fill="#fff" />
      <text x={60} y={58} fontSize={7} textAnchor="middle" fill={MUTED} fontFamily="ui-monospace, monospace">
        contract.mint()
      </text>
    </>
  ),
  'live-stat': (a) => (
    <>
      {card(14, 10, 92, 56, 6)}
      {muted(22, 19, 34)}
      {dot('#3ecf8e', 94, 21, 3)}
      <circle cx={94} cy={21} r={5.5} fill="none" stroke="#3ecf8e" strokeWidth={0.8} opacity={0.6} />
      <text x={22} y={46} fontSize={20} fontWeight={800} fill={INK} fontFamily="system-ui, sans-serif">
        1,284
      </text>
      <path d="M22 58 L36 54 L48 57 L62 50 L76 53 L96 46" stroke={a} strokeWidth={1.6} fill="none" strokeLinecap="round" strokeLinejoin="round" />
    </>
  ),
  'contract-form': (a) => (
    <>
      {card(10, 4, 100, 68, 6)}
      <text x={18} y={15} fontSize={7.5} fontWeight={700} fill={a} fontFamily="ui-monospace, monospace">
        join(addr, n)
      </text>
      {muted(18, 20, 20, 2)}
      {field(18, 24, 84, 9)}
      {muted(22, 27.5, 40)}
      {muted(18, 37, 14, 2)}
      {field(18, 41, 84, 9)}
      <text x={22} y={48} fontSize={6.5} fill={INK} fontFamily="ui-monospace, monospace">
        25
      </text>
      {btn(a, 18, 56, 84, 11)}
      <path d="M55 61.5 h8 m-3 -3 l3 3 l-3 3" stroke="#fff" strokeWidth={1.3} fill="none" strokeLinecap="round" />
    </>
  ),

  // ---- Forms ----------------------------------------------------------------------------
  'form-text': (a) => (
    <>
      {muted(18, 22, 28, 2.5)}
      {field(18, 28, 84, 14)}
      {muted(24, 34, 36)}
      <line x1={64} y1={31} x2={64} y2={39} stroke={a} strokeWidth={1.5} />
    </>
  ),
  'form-amount': (a) => (
    <>
      {muted(18, 22, 28, 2.5)}
      {field(18, 28, 84, 14)}
      <text x={24} y={38.5} fontSize={9} fontWeight={700} fill={INK} fontFamily="system-ui, sans-serif">
        25.00
      </text>
      <rect x={78} y={31} width={20} height={8} rx={4} fill={a} opacity={0.3} />
      <rect x={82} y={34} width={12} height={2} rx={1} fill={a} />
    </>
  ),
  'form-address': (a) => (
    <>
      {muted(18, 22, 28, 2.5)}
      {field(18, 28, 84, 14)}
      <text x={24} y={38} fontSize={7.5} fontWeight={600} fill={INK} fontFamily="ui-monospace, monospace">
        SQ:fdd…ac3b
      </text>
      {dot(a, 94, 35, 3.5)}
      <path d="M92.5 35 l1.2 1.2 l2 -2.4" stroke="#fff" strokeWidth={1} fill="none" />
    </>
  ),
  'form-select': (a) => (
    <>
      {muted(18, 14, 28, 2.5)}
      {field(18, 20, 84, 12)}
      {muted(24, 25, 30)}
      <path d="M92 24.5 l3 3 l3 -3" stroke={a} strokeWidth={1.4} fill="none" strokeLinecap="round" />
      {card(18, 35, 84, 30, 3)}
      <rect x={20} y={38} width={80} height={8} rx={2} fill={a} opacity={0.25} />
      {muted(24, 41, 30)}
      {muted(24, 51, 36)}
      {muted(24, 59, 26)}
    </>
  ),
  'form-toggle': (a) => (
    <>
      {text(18, 26, 40, 3)}
      {muted(18, 33, 30, 2)}
      <rect x={76} y={24} width={26} height={14} rx={7} fill={a} />
      <circle cx={95} cy={31} r={5} fill="#fff" />
      {text(18, 48, 34, 3, 0.5)}
      <rect x={76} y={46} width={26} height={14} rx={7} fill={LINE} />
      <circle cx={83} cy={53} r={5} fill={MUTED} />
    </>
  ),
};

/** Category fallback for pieces without a dedicated drawing. */
const FALLBACK: Record<string, string> = {
  layout: 'card',
  wallet: 'account-card',
  tokens: 'token-info',
  auth: 'sign-in',
  activity: 'pending-tx',
  nft: 'nft-gallery',
  modules: 'contract-call',
  forms: 'form-text',
};

export function hasPieceArt(id: string): boolean {
  return id in ART;
}

/** Illustrated mini-mockup of a palette piece, drawn in the shelf's accent colour. */
export function PieceArt({ id, category, tone, className }: { id: string; category: string; tone: string; className?: string }) {
  const accent = TONE_COLORS[tone] ?? TONE_COLORS.violet!;
  const draw = ART[id] ?? ART[FALLBACK[category] ?? 'card']!;
  const uid = `pa${useId().replace(/:/g, '')}`;
  return (
    <svg className={className} viewBox="0 0 120 76" role="presentation" aria-hidden="true" preserveAspectRatio="xMidYMid meet">
      {draw(accent, uid)}
    </svg>
  );
}
