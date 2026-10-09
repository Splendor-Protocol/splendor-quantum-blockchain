import { Link } from 'react-router-dom';
import { useRegistry } from '../registry';

export function HomePage() {
  const { templates, catalog, recipes } = useRegistry();
  return (
    <div className="pg-page">
      <section className="pg-hero">
        <h1>Build a post-quantum dApp in minutes</h1>
        <p>
          Learn the Splendor SDK, try every widget live, then assemble your own dApp from blocks and export a project
          that runs with <code>npm install &amp;&amp; npm run dev</code>.
        </p>
        <div className="pg-row">
          <Link className="pg-btn" to="/build">
            Start building
          </Link>
          <Link className="pg-btn pg-btn-ghost" to="/learn">
            Learn the basics
          </Link>
        </div>
      </section>

      <section className="pg-cards">
        <Link to="/learn" className="pg-card">
          <h2>Learn</h2>
          <p>Guides and {recipes.length} runnable SDK recipes against testnet or devnet.</p>
        </Link>
        <Link to="/widgets" className="pg-card">
          <h2>Widgets</h2>
          <p>{catalog.length} widgets and blocks, each with a live preview, settings and copy-paste code.</p>
        </Link>
        <Link to="/build" className="pg-card">
          <h2>Build</h2>
          <p>Pages, blocks, theme, mock or live wallet preview, then export to zip, StackBlitz, Vercel or Netlify.</p>
        </Link>
      </section>

      <section>
        <h2>Start from a template</h2>
        <div className="pg-cards">
          {templates.map((t) => (
            <Link key={t.id} to={`/build?template=${t.id}`} className="pg-card" data-testid={`template-${t.id}`}>
              <h3>{t.title}</h3>
              <p>{t.description}</p>
            </Link>
          ))}
        </div>
      </section>
    </div>
  );
}
