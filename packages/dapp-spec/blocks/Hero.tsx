export type HeroProps = {
  heading?: string;
  text?: string;
};

/** Page heading with a short description. */
export function Hero({ heading = 'My Splendor dApp', text = '' }: HeroProps) {
  return (
    <section className="sqc-widget dapp-hero" data-sqc-widget="hero">
      <h2 className="sqc-widget__title">{heading}</h2>
      {text ? <p className="sqc-widget__msg">{text}</p> : null}
    </section>
  );
}
