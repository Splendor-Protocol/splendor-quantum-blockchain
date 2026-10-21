import { useState } from 'react';
import { AmountInput } from '@splendor/connect-react';

export type FormAmountInputProps = {
  label?: string;
  placeholder?: string;
  symbol?: string;
};

/** Decimal amount field with built-in validation (local state only). */
export function FormAmountInput({ label = 'Amount', placeholder = '0.0', symbol = 'SPLD' }: FormAmountInputProps) {
  const [value, setValue] = useState('');
  return (
    <div data-sqc-widget="form-amount">
      <AmountInput label={label} value={value} onChange={setValue} placeholder={placeholder} symbol={symbol} />
    </div>
  );
}
