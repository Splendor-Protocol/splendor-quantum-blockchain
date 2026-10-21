export type ImageBlockProps = {
  src?: string;
  alt?: string;
  caption?: string;
};

/** Remote image with optional caption (https only). */
export function ImageBlock({ src = '', alt = '', caption = '' }: ImageBlockProps) {
  const safe = /^https:\/\//i.test(src) ? src : '';
  return (
    <figure className="sqc-widget dapp-image" data-sqc-widget="image-block" style={{ margin: 0 }}>
      {safe ? (
        <img src={safe} alt={alt} style={{ width: '100%', borderRadius: 'var(--sqc-radius, 12px)', display: 'block' }} />
      ) : (
        <div style={{ padding: '2rem', textAlign: 'center', background: 'var(--sqc-surface, #12151d)', borderRadius: 12, color: 'var(--sqc-text-muted)' }}>
          Set an https image URL
        </div>
      )}
      {caption ? <figcaption className="sqc-widget__msg" style={{ marginTop: '0.5rem' }}>{caption}</figcaption> : null}
    </figure>
  );
}
