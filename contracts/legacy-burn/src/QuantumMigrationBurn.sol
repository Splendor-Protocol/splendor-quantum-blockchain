// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @dev Minimal local ERC-20 surface. Only the two functions the burn path
///      needs; vendored here so the project has zero external dependencies.
interface IERC20 {
    function balanceOf(address account) external view returns (uint256);
    function transferFrom(address from, address to, uint256 value) external returns (bool);
}

/**
 * @title QuantumMigrationBurn
 * @notice One-way burn gate for the SPLD/SPLV migration from the legacy
 *         Splendor mainnet (EVM chain 2691) to the Splendor Quantum chain.
 *
 *         Users burn at least 1 whole token here — native SPLD via
 *         {burnSpld}, ERC-20 SPLV via {burnSplv} — naming the quantum
 *         recipient as the 32 raw bytes of their `SQ:` address. Every burn
 *         forwards the full amount straight to the dead address and emits a
 *         single {Burned} event; an off-chain relayer reads the event (after
 *         a confirmation depth) and credits the same amount on the quantum
 *         chain.
 *
 * @dev Deliberately NON-upgradeable and custody-free:
 *      - No proxy, no initializer — a plain constructor. There is nothing an
 *        upgrade could steal and no "rescue" story to erode trust in the burn.
 *      - The contract never holds funds: SPLD is forwarded to {BURN_SINK} in
 *        the same call, SPLV is pulled with `transferFrom` directly to
 *        {BURN_SINK}. There are no withdraw/rescue/sweep functions of any
 *        kind, and {receive}/{fallback} revert so nothing can be parked here.
 *      - Two hand-rolled roles instead of AccessControl: `owner` (sets the
 *        pauser, two-step ownership transfer) and `pauser` (pause/unpause,
 *        the incident brake). No deadline — the migration is open-ended.
 */
contract QuantumMigrationBurn {
    // ---------------------------------------------------------------------
    // Errors
    // ---------------------------------------------------------------------

    /// @dev Caller is not the {owner}.
    error NotOwner();
    /// @dev Caller is not the {pauser}.
    error NotPauser();
    /// @dev Caller is not the {pendingOwner}.
    error NotPendingOwner();
    /// @dev A zero address was supplied where a real one is required.
    error ZeroAddress();
    /// @dev The SPLV address has no deployed code.
    error SplvNotContract();
    /// @dev Action requires the contract to not be paused.
    error EnforcedPause();
    /// @dev Action requires the contract to be paused.
    error ExpectedPause();
    /// @dev Burn amount is below {MIN_BURN}.
    error BelowMinimumBurn();
    /// @dev Burn amount is above {maxBurn} — it could never be claimed on the
    ///      quantum side, so it is refused here rather than destroyed.
    error AboveMaximumBurn();
    /// @dev Constructor was given a maxBurn below {MIN_BURN}.
    error InvalidMaxBurn();
    /// @dev The quantum recipient is bytes32(0).
    error ZeroQuantumRecipient();
    /// @dev Reentrant call detected.
    error ReentrantCall();
    /// @dev Forwarding native SPLD to {BURN_SINK} failed.
    error BurnForwardFailed();
    /// @dev SPLV `transferFrom` failed or returned false.
    error SplvTransferFailed();
    /// @dev {BURN_SINK} received a different SPLV amount than requested
    ///      (fee-on-transfer or otherwise non-standard token behaviour).
    error UnexpectedSplvAmount();
    /// @dev The contract never accepts direct transfers or unknown calls.
    error DirectTransferRejected();

    // ---------------------------------------------------------------------
    // Constants / immutables
    // ---------------------------------------------------------------------

    /// @notice Asset discriminator for native SPLD in {Burned}.
    uint8 public constant ASSET_SPLD = 0;
    /// @notice Asset discriminator for ERC-20 SPLV in {Burned}.
    uint8 public constant ASSET_SPLV = 1;

    /// @notice Minimum burn per transaction, both assets: 1 whole token.
    uint256 public constant MIN_BURN = 1e18;

    /// @notice Maximum burn per transaction, both assets. MUST equal the
    ///         quantum bridge's `max_claim_amount`: the quantum module refuses
    ///         to credit a claim larger than that, so a burn above it would be
    ///         destroyed on this chain and never claimable. Enforcing the same
    ///         ceiling here makes an un-creditable burn structurally
    ///         impossible; a larger migration is done as several burns.
    ///         Immutable — changing it (or the claim cap) means redeploying.
    uint256 public immutable maxBurn;

    /// @notice Where every burn ends up. No key exists for this address.
    address public constant BURN_SINK = 0x000000000000000000000000000000000000dEaD;

    /// @notice The legacy SPLV ERC-20 token being migrated.
    IERC20 public immutable splv;

    // ---------------------------------------------------------------------
    // Storage
    // ---------------------------------------------------------------------

    /// @notice Current owner: sets the pauser, transfers ownership two-step.
    address public owner;
    /// @notice Address nominated by {transferOwnership}, pending {acceptOwnership}.
    address public pendingOwner;
    /// @notice Incident brake operator: may {pause} and {unpause}.
    address public pauser;
    /// @notice True while burns are halted.
    bool public paused;

    /// @notice Total number of burns; the `id` of the last {Burned} event.
    /// @dev Shared across both assets, first id is 1.
    uint256 public burnCount;

    uint256 private constant _NOT_ENTERED = 1;
    uint256 private constant _ENTERED = 2;
    uint256 private _reentrancyStatus = _NOT_ENTERED;

    // ---------------------------------------------------------------------
    // Events
    // ---------------------------------------------------------------------

    /// @notice One burn, the relayer's unit of work.
    /// @param id Monotonic counter starting at 1, shared across both assets.
    /// @param sender The account that burned.
    /// @param asset 0 = native SPLD, 1 = ERC-20 SPLV.
    /// @param amount Exact amount delivered to {BURN_SINK}, in 1e18 units.
    /// @param quantumRecipient 32 raw bytes of the destination `SQ:` address.
    event Burned(
        uint256 indexed id, address indexed sender, uint8 indexed asset, uint256 amount, bytes32 quantumRecipient
    );

    event Paused(address indexed account);
    event Unpaused(address indexed account);
    event PauserUpdated(address indexed previousPauser, address indexed newPauser);
    event OwnershipTransferStarted(address indexed previousOwner, address indexed newOwner);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    // ---------------------------------------------------------------------
    // Modifiers
    // ---------------------------------------------------------------------

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    modifier onlyPauser() {
        if (msg.sender != pauser) revert NotPauser();
        _;
    }

    modifier whenNotPaused() {
        if (paused) revert EnforcedPause();
        _;
    }

    modifier nonReentrant() {
        if (_reentrancyStatus == _ENTERED) revert ReentrantCall();
        _reentrancyStatus = _ENTERED;
        _;
        _reentrancyStatus = _NOT_ENTERED;
    }

    // ---------------------------------------------------------------------
    // Construction
    // ---------------------------------------------------------------------

    /// @param splv_ The legacy SPLV ERC-20 token (must have code).
    /// @param pauser_ Initial pauser (must be non-zero; the incident brake
    ///        should exist from block one).
    /// @param maxBurn_ Maximum burn per transaction; MUST equal the quantum
    ///        bridge's max_claim_amount (see {maxBurn}). Must be >= {MIN_BURN}.
    constructor(address splv_, address pauser_, uint256 maxBurn_) {
        if (splv_ == address(0) || pauser_ == address(0)) revert ZeroAddress();
        if (splv_.code.length == 0) revert SplvNotContract();
        if (maxBurn_ < MIN_BURN) revert InvalidMaxBurn();

        splv = IERC20(splv_);
        maxBurn = maxBurn_;
        owner = msg.sender;
        pauser = pauser_;

        emit OwnershipTransferred(address(0), msg.sender);
        emit PauserUpdated(address(0), pauser_);
    }

    // ---------------------------------------------------------------------
    // Burns
    // ---------------------------------------------------------------------

    /// @notice Burns native SPLD for migration: forwards the full `msg.value`
    ///         to {BURN_SINK} and emits {Burned} with asset = 0.
    /// @dev Forwards `msg.value` (never `address(this).balance`), so no burn
    ///      path leaves the contract holding funds. Value force-sent via
    ///      selfdestruct/coinbase is unreachable by design and never credited.
    /// @param quantumRecipient 32 raw bytes of the destination `SQ:` address
    ///        (the hex after the `SQ:` prefix). Must not be zero.
    function burnSpld(bytes32 quantumRecipient) external payable whenNotPaused nonReentrant {
        if (msg.value < MIN_BURN) revert BelowMinimumBurn();
        if (msg.value > maxBurn) revert AboveMaximumBurn();
        if (quantumRecipient == bytes32(0)) revert ZeroQuantumRecipient();

        uint256 id = ++burnCount;

        (bool ok,) = BURN_SINK.call{value: msg.value}("");
        if (!ok) revert BurnForwardFailed();

        emit Burned(id, msg.sender, ASSET_SPLD, msg.value, quantumRecipient);
    }

    /// @notice Burns SPLV for migration: pulls `amount` from the caller
    ///         straight to {BURN_SINK} via `transferFrom` and emits {Burned}
    ///         with asset = 1. Requires a prior approval of at least `amount`
    ///         to this contract.
    /// @dev Verifies {BURN_SINK} received exactly `amount` (balance snapshot
    ///      before/after) so fee-on-transfer or rebasing behaviour cannot
    ///      credit more on the quantum side than was actually burned.
    ///      Handles non-standard ERC-20s that return no value.
    /// @param amount SPLV amount to burn, in 1e18 units; at least {MIN_BURN}.
    /// @param quantumRecipient 32 raw bytes of the destination `SQ:` address.
    ///        Must not be zero.
    function burnSplv(uint256 amount, bytes32 quantumRecipient) external whenNotPaused nonReentrant {
        if (amount < MIN_BURN) revert BelowMinimumBurn();
        if (amount > maxBurn) revert AboveMaximumBurn();
        if (quantumRecipient == bytes32(0)) revert ZeroQuantumRecipient();

        uint256 sinkBefore = splv.balanceOf(BURN_SINK);
        _safeTransferFrom(msg.sender, BURN_SINK, amount);
        uint256 received = splv.balanceOf(BURN_SINK) - sinkBefore;
        if (received != amount) revert UnexpectedSplvAmount();

        uint256 id = ++burnCount;
        emit Burned(id, msg.sender, ASSET_SPLV, amount, quantumRecipient);
    }

    // ---------------------------------------------------------------------
    // Pause (incident brake)
    // ---------------------------------------------------------------------

    /// @notice Halts both burn paths. Only the {pauser}.
    function pause() external onlyPauser {
        if (paused) revert EnforcedPause();
        paused = true;
        emit Paused(msg.sender);
    }

    /// @notice Resumes both burn paths. Only the {pauser}.
    function unpause() external onlyPauser {
        if (!paused) revert ExpectedPause();
        paused = false;
        emit Unpaused(msg.sender);
    }

    // ---------------------------------------------------------------------
    // Role management
    // ---------------------------------------------------------------------

    /// @notice Replaces the pauser. Only the {owner}.
    /// @param newPauser The new pauser; must be non-zero (the incident brake
    ///        must always have a holder).
    function setPauser(address newPauser) external onlyOwner {
        if (newPauser == address(0)) revert ZeroAddress();
        emit PauserUpdated(pauser, newPauser);
        pauser = newPauser;
    }

    /// @notice Starts a two-step ownership transfer. Only the {owner}.
    /// @dev The nominee must call {acceptOwnership}; nominating address(0)
    ///      cancels any pending transfer.
    /// @param newOwner The nominated owner.
    function transferOwnership(address newOwner) external onlyOwner {
        pendingOwner = newOwner;
        emit OwnershipTransferStarted(owner, newOwner);
    }

    /// @notice Completes the ownership transfer. Only the {pendingOwner}.
    function acceptOwnership() external {
        if (msg.sender != pendingOwner) revert NotPendingOwner();
        emit OwnershipTransferred(owner, msg.sender);
        owner = msg.sender;
        pendingOwner = address(0);
    }

    // ---------------------------------------------------------------------
    // No direct transfers, ever
    // ---------------------------------------------------------------------

    /// @notice Rejects plain native transfers; the contract must never hold
    ///         funds. Burn via {burnSpld} instead.
    receive() external payable {
        revert DirectTransferRejected();
    }

    /// @notice Rejects unknown calls (with or without value).
    fallback() external payable {
        revert DirectTransferRejected();
    }

    // ---------------------------------------------------------------------
    // Internals
    // ---------------------------------------------------------------------

    /// @dev `safeTransferFrom`-style call on {splv}: succeeds when the call
    ///      succeeds and returns either nothing (non-standard ERC-20s) or
    ///      `true`. Reverts {SplvTransferFailed} otherwise.
    function _safeTransferFrom(address from, address to, uint256 value) private {
        (bool ok, bytes memory data) = address(splv).call(abi.encodeCall(IERC20.transferFrom, (from, to, value)));
        if (!ok || (data.length != 0 && !abi.decode(data, (bool)))) revert SplvTransferFailed();
    }
}
