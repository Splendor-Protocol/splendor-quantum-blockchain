export type RichTextProps = { text?: string };

/** Plain paragraph for page copy. */
export function RichText({ text = '' }: RichTextProps) {
  return (
    <p className="sqc-widget dapp-rich-text" data-sqc-widget="rich-text">
      {text || 'Add some text.'}
    </p>
  );
}
