import { Link } from 'react-router-dom';
import type { DappTemplate } from '@splendor/dapp-spec';
import { PieceArt } from '../studio/PieceArt';
import { groupMeta } from '../registry/groups';

/** First hero: what it is, why post-quantum matters, and one obvious next step. */
export function ProductHero({ studioHref, onNewProject }: { studioHref: string; onNewProject: () => void }) {
  return (
    <section className="pg-hero pg-hero-product" aria-labelledby="home-title">
      <div className="pg-hero-copy">
        <p className="pg-eyebrow">dApp Studio for Splendor Quantum Chain</p>
        <h1 id="home-title">Build quantum-safe dApps without writing the plumbing</h1>
        <p className="pg-hero-lead">
          Drag wallet, token, NFT and contract pieces onto a live page, test them with a mock or a real wallet, then export a Vite or
          Next.js project and deploy it to your own GitHub, Vercel or Netlify. Every signature is ML-DSA-65 (post-quantum).
        </p>
        <div className="pg-row pg-hero-ctas">
          <button type="button" className="pg-btn pg-btn-lg" data-testid="new-project" onClick={onNewProject}>
            Start a new dApp
          </button>
          <Link className="pg-btn pg-btn-ghost pg-btn-lg" to={studioHref}>
            Open the studio
          </Link>
        </div>
        <ul className="pg-hero-points" aria-label="Highlights">
          <li>No account needed to start</li>
          <li>Real code you own</li>
          <li>Testnet and devnet built in</li>
        </ul>
      </div>
      <StudioMock />
    </section>
  );
}

/** Illustration of the studio (palette, live page, inspector), drawn from the same piece art. */
function StudioMock() {
  const pieces = ['hero', 'token-stats', 'nft-panel', 'transfer', 'validator-list', 'receive'];
  return (
    <div className="pg-studio-mock" aria-hidden="true">
      <div className="pg-studio-mock-bar">
        <span />
        <span />
        <span />
      </div>
      <div className="pg-studio-mock-body">
        <div className="pg-studio-mock-palette">
          {pieces.map((id) => (
            <PieceArt key={id} id={id} category="layout" tone="violet" className="pg-studio-mock-tile" />
          ))}
        </div>
        <div className="pg-studio-mock-canvas">
          <PieceArt id="hero-gradient" category="layout" tone="violet" />
          <div className="pg-studio-mock-row">
            <PieceArt id="token-stats" category="tokens" tone="amber" />
            <PieceArt id="nft721-gallery" category="nft" tone="pink" />
          </div>
        </div>
        <div className="pg-studio-mock-inspector">
          {[70, 52, 86, 40, 64].map((w, i) => (
            <span key={i} style={{ width: `${w}%` }} />
          ))}
        </div>
      </div>
    </div>
  );
}

const STEPS = [
  { title: 'Pick a starting point', text: 'A ready-made app (token project, NFT membership, contract app), a template, or a blank page.' },
  { title: 'Build on a live page', text: 'Drop pieces, bind your contracts, edit text and style, add pages. The test wallet answers every call.' },
  { title: 'Export and deploy', text: 'Download a typed Vite or Next.js project, open it in StackBlitz, or push it to GitHub and import it on Vercel or Netlify.' },
];

export function HowItWorks() {
  return (
    <section aria-labelledby="home-how" className="pg-how">
      <h2 id="home-how">How it works</h2>
      <ol className="pg-steps-list">
        {STEPS.map((s, i) => (
          <li key={s.title}>
            <span className="pg-step-num" aria-hidden="true">
              {i + 1}
            </span>
            <strong>{s.title}</strong>
            <p className="pg-muted">{s.text}</p>
          </li>
        ))}
      </ol>
    </section>
  );
}

const FEATURES = [
  { art: 'transfer', title: 'Wallet and tokens', text: 'Connect, balances, SPLD and QRC-20 transfers with real fee estimates and review steps.' },
  { art: 'nft-panel', title: 'NFTs and chain modules', text: 'QRC-721 and QRC-1155 galleries, bridge status, validator directory, x402 paywalls and activity feeds.' },
  { art: 'contract-form', title: 'Your contracts', text: 'Paste an ABI: every write gets a form, every read a live value. Typed clients come with the export.' },
  { art: 'members', title: 'Pages and access', text: 'Multi-page apps with public, wallet-only and signed-in pages, navigation and a 404 page.' },
  { art: 'hero-gradient', title: 'Themes that fit', text: 'Presets, colours, radius and fonts, previewed on desktop, tablet and phone sizes.' },
  { art: 'receive', title: 'Ship it', text: 'Zip, StackBlitz, GitHub push, then Vercel or Netlify import with RPC rewrites and a strict CSP included.' },
];

export function FeatureGrid() {
  return (
    <section aria-labelledby="home-features">
      <h2 id="home-features">Everything a dApp needs</h2>
      <div className="pg-feature-grid">
        {FEATURES.map((f) => (
          <article key={f.title} className="pg-feature">
            <PieceArt id={f.art} category="layout" tone="violet" className="pg-feature-art" />
            <h3>{f.title}</h3>
            <p className="pg-muted">{f.text}</p>
          </article>
        ))}
      </div>
    </section>
  );
}

/** The first piece of a template, drawn as its thumbnail. */
function templateArt(t: DappTemplate): { id: string; category: string } {
  const first = t.spec.pages[0]?.blocks?.find((b) => b.widget !== 'hero' && b.widget !== 'hero-gradient') ?? t.spec.pages[0]?.blocks?.[0];
  return { id: first?.widget ?? 'hero', category: 'layout' };
}

export function TemplateShowcase({
  templates,
  buildHref,
  onCustomize,
}: {
  templates: readonly DappTemplate[];
  buildHref: (id: string) => string;
  onCustomize: (id: string) => void;
}) {
  return (
    <section aria-labelledby="home-templates">
      <h2 id="home-templates">Start from a template</h2>
      <div className="pg-template-grid">
        {templates.map((t) => {
          const art = templateArt(t);
          return (
            <div key={t.id} className="pg-template-card">
              <Link to={buildHref(t.id)} className="pg-template-link" data-testid={`template-${t.id}`}>
                <span className="pg-template-thumb">
                  <PieceArt id={art.id} category={art.category} tone={groupMeta(art.category).tone} />
                </span>
                <strong>{t.title}</strong>
                <span className="pg-muted">{t.description}</span>
              </Link>
              <button type="button" className="pg-link-btn" data-testid={`customize-${t.id}`} onClick={() => onCustomize(t.id)}>
                Choose theme and name first
              </button>
            </div>
          );
        })}
      </div>
    </section>
  );
}

const FAQ = [
  ['Do I need an account?', 'No. Projects stay in this browser until you sign in with your wallet; then they sync to your account with version history.'],
  ['Is the exported code mine?', 'Yes. It is a normal Vite or Next.js project using the open @splendor packages. Host it anywhere.'],
  ['What does post-quantum mean here?', 'Splendor accounts sign with ML-DSA-65, a NIST post-quantum signature scheme, so the keys are not broken by future quantum computers.'],
  ['Can I try transactions without funds?', 'Yes. The test wallet answers every call in the preview. Switch to Real wallet to send on testnet or devnet.'],
  ['How do I deploy?', 'Export the project, push it to GitHub from the Export dialog, then import the repository on Vercel or Netlify. RPC rewrites and security headers are generated for you.'],
];

export function Faq() {
  return (
    <section aria-labelledby="home-faq" className="pg-faq">
      <h2 id="home-faq">Questions</h2>
      {FAQ.map(([q, a]) => (
        <details key={q}>
          <summary>{q}</summary>
          <p className="pg-muted">{a}</p>
        </details>
      ))}
    </section>
  );
}
