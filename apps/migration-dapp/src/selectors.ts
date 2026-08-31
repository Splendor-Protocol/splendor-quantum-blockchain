/**
 * Precomputed keccak-256 constants for the QuantumMigrationBurn contract.
 *
 * Neither the Worker nor the page ships a keccak implementation — these are
 * computed at build/author time and pinned here. `test/selectors.test.ts`
 * recomputes every one of them with @noble/hashes (a devDependency only), so a
 * typo can never survive `npm test`. The page's inline script hardcodes the
 * same literals (page.test.ts asserts they match these).
 */

/** burnSpld(bytes32) — payable, burns native SPLD to 0x…dEaD. */
export const SEL_BURN_SPLD = '0xb4b27926';

/** burnSplv(uint256,bytes32) — pulls SPLV via transferFrom to 0x…dEaD. */
export const SEL_BURN_SPLV = '0xe5e2a80a';

/** approve(address,uint256) — ERC-20. */
export const SEL_APPROVE = '0x095ea7b3';

/** allowance(address,address) — ERC-20. */
export const SEL_ALLOWANCE = '0xdd62ed3e';

/** balanceOf(address) — ERC-20. */
export const SEL_BALANCE_OF = '0x70a08231';

/**
 * topic0 of `Burned(uint256 indexed id, address indexed sender,
 * uint8 indexed asset, uint256 amount, bytes32 quantumRecipient)`.
 * keccak256("Burned(uint256,address,uint8,uint256,bytes32)")
 */
export const BURNED_TOPIC0 =
  '0x18fb7d6af55f185ad81a39bf0537706c6c62595e260cf5aaeb3f491a4240cd58';
