import { NavLink, Navigate, useLocation, useNavigate, useParams, Routes, Route } from 'react-router-dom';
import { useRegistry } from '../registry';
import { GUIDES } from './guides';
import { Markdown } from './Markdown';
import { RecipeRunner } from './RecipeRunner';

function GuideView() {
  const { id } = useParams();
  const guide = GUIDES.find((g) => g.id === id);
  if (!guide) return <Navigate to="/learn/guide/widgets" replace />;
  return <Markdown source={guide.markdown} />;
}

function RecipeView() {
  const { id } = useParams();
  const { recipes } = useRegistry();
  const recipe = recipes.find((r) => r.id === id);
  if (!recipe) return <p className="pg-muted">Recipe not found.</p>;
  return <RecipeRunner key={recipe.id} recipe={recipe} />;
}

export function LearnPage() {
  const { recipes } = useRegistry();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const groups = [...new Set(GUIDES.map((g) => g.group))];
  const modules = [...new Set(recipes.map((r) => r.module))];
  return (
    <div className="pg-workspace-2 pg-learn">
      <aside className="pg-sidebar pg-panel">
        {/* Phones: one picker instead of a long list above the article. */}
        <label className="pg-learn-jump">
          <span className="pg-muted">Topic</span>
          <select aria-label="Learn topic" value={pathname.replace(/\/$/, '')} onChange={(e) => navigate(e.target.value)} data-testid="learn-jump">
            {groups.map((g) => (
              <optgroup key={g} label={g}>
                {GUIDES.filter((x) => x.group === g).map((x) => (
                  <option key={x.id} value={`/learn/guide/${x.id}`}>
                    {x.title}
                  </option>
                ))}
              </optgroup>
            ))}
            {modules.map((m) => (
              <optgroup key={m} label={`Recipes · ${m}`}>
                {recipes
                  .filter((r) => r.module === m)
                  .map((r) => (
                    <option key={r.id} value={`/learn/recipe/${r.id}`}>
                      {r.title}
                    </option>
                  ))}
              </optgroup>
            ))}
          </select>
        </label>
        <nav className="pg-learn-nav" aria-label="Learn">
          {groups.map((g) => (
            <div key={g}>
              <p className="pg-list-title">{g}</p>
              <ul className="pg-list">
                {GUIDES.filter((x) => x.group === g).map((x) => (
                  <li key={x.id}>
                    <NavLink to={`/learn/guide/${x.id}`} className={({ isActive }) => (isActive ? 'is-active' : '')}>
                      {x.title}
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>
          ))}
          <div>
            <p className="pg-list-title">Runnable recipes</p>
            {modules.map((m) => (
              <ul key={m} className="pg-list">
                <li className="pg-muted" style={{ fontSize: '0.8rem', padding: '0.3rem 0.6rem 0' }}>
                  {m}
                </li>
                {recipes
                  .filter((r) => r.module === m)
                  .map((r) => (
                    <li key={r.id}>
                      <NavLink to={`/learn/recipe/${r.id}`} className={({ isActive }) => (isActive ? 'is-active' : '')}>
                        {r.title}
                      </NavLink>
                    </li>
                  ))}
              </ul>
            ))}
          </div>
        </nav>
      </aside>
      <section className="pg-panel pg-learn-body">
        <Routes>
          <Route index element={<Navigate to="guide/widgets" replace />} />
          <Route path="guide/:id" element={<GuideView />} />
          <Route path="recipe/:id" element={<RecipeView />} />
        </Routes>
      </section>
    </div>
  );
}
