export type HeadingProps = {
  text?: string;
  level?: string;
};

/** Standalone heading (h1–h3). */
export function Heading({ text = 'Heading', level = '2' }: HeadingProps) {
  const Tag = (level === '1' ? 'h1' : level === '3' ? 'h3' : 'h2') as 'h1' | 'h2' | 'h3';
  return (
    <Tag className="sqc-widget dapp-heading" data-sqc-widget="heading">
      {text}
    </Tag>
  );
}
