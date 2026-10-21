import { useState } from 'react';
import { AddressInput } from '@splendor/connect-react';

export type FormAddressInputProps = {
  label?: string;
  placeholder?: string;
};

/** `SQ:` address field with built-in validation (local state only). */
export function FormAddressInput({ label = 'Address', placeholder = 'SQ:…' }: FormAddressInputProps) {
  const [value, setValue] = useState('');
  return (
    <div data-sqc-widget="form-address">
      <AddressInput label={label} value={value} onChange={setValue} placeholder={placeholder} />
    </div>
  );
}
