import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@splendor/connect-react/style.css';
import '../styles.css';
import { PreviewRuntime } from './PreviewRuntime';

// The preview iframe's own entry (preview.html): the renderer and the mock wallet, without the
// playground's router, pages or session.
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <PreviewRuntime />
  </StrictMode>,
);
