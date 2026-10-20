import { Component, type ReactNode } from 'react';

/**
 * Keeps one broken page from blanking the whole app: the header stays usable and the user can
 * retry or go home. `resetKey` (the route) clears the error on navigation.
 */
export class AppErrorBoundary extends Component<{ resetKey: string; children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidUpdate(prev: { resetKey: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  componentDidCatch(error: Error) {
    console.error('[playground] page crashed', error);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="pg-page" role="alert">
        <h1>Something went wrong on this page</h1>
        <p className="pg-muted">Your builder draft is saved in this browser. Try again, or go back to the start.</p>
        <div className="pg-row">
          <button type="button" className="pg-btn" onClick={() => this.setState({ error: null })}>
            Try again
          </button>
          <a className="pg-btn pg-btn-ghost" href="/">
            Home
          </a>
        </div>
      </div>
    );
  }
}
