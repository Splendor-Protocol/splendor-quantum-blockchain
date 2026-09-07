// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Script} from "forge-std/Script.sol";
import {console2} from "forge-std/console2.sol";
import {QuantumMigrationBurn} from "../src/QuantumMigrationBurn.sol";

/**
 * @title DeployQuantumMigrationBurn
 * @notice Deploys {QuantumMigrationBurn} on the legacy Splendor mainnet
 *         (EVM chain 2691). The broadcasting key becomes `owner`.
 *
 * Required environment variables:
 * - `SPLV_TOKEN`  — the legacy SPLV ERC-20 address
 *                   (mainnet 2691: 0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689).
 * - `BURN_PAUSER` — the address holding the pause/unpause incident brake.
 * - `MAX_BURN`    — maximum burn per tx, in wei. MUST equal the quantum
 *                   bridge instance's `max_claim_amount` (read it from
 *                   GET /bridge/{addr}) — a burn above it is uncreditable.
 *                   Must be >= 1e18.
 *
 * Example (run from wallet/contracts, the established 2691 Foundry home,
 * after copying src/ + script/ there — see README.md):
 *
 *   SPLV_TOKEN=0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689 \
 *   BURN_PAUSER=0x... \
 *   MAX_BURN=100000000000000000000000 \
 *   forge script script/DeployQuantumMigrationBurn.s.sol \
 *     --rpc-url splendor_mainnet --broadcast --verify
 */
contract DeployQuantumMigrationBurn is Script {
    function run() external returns (QuantumMigrationBurn burn) {
        address splvToken = vm.envAddress("SPLV_TOKEN");
        address pauser = vm.envAddress("BURN_PAUSER");
        uint256 maxBurn = vm.envUint("MAX_BURN");

        vm.startBroadcast();
        burn = new QuantumMigrationBurn(splvToken, pauser, maxBurn);
        vm.stopBroadcast();

        console2.log("QuantumMigrationBurn:", address(burn));
        console2.log("  splv:   ", splvToken);
        console2.log("  owner:  ", burn.owner());
        console2.log("  pauser: ", pauser);
        console2.log("  maxBurn:", maxBurn);
    }
}
