export type TestimonialProps = {
  quote?: string;
  author?: string;
  role?: string;
};

/** Customer quote with attribution. */
export function Testimonial({ quote = 'Shipping on Splendor took an afternoon.', author = 'Ada', role = 'Founder' }: TestimonialProps) {
  return (
    <figure className="sqc-widget dapp-testimonial" data-sqc-widget="testimonial" style={{ margin: 0, padding: '1.5rem' }}>
      <blockquote style={{ margin: 0, fontSize: '1.15rem', lineHeight: 1.5 }}>“{quote}”</blockquote>
      {author || role ? (
        <figcaption className="sqc-widget__msg" style={{ marginTop: '0.9rem' }}>
          <strong>{author}</strong>
          {role ? <span> · {role}</span> : null}
        </figcaption>
      ) : null}
    </figure>
  );
}
