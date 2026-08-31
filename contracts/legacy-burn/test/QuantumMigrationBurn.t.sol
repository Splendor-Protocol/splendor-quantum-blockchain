// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Test} from "forge-std/Test.sol";
import {QuantumMigrationBurn} from "../src/QuantumMigrationBurn.sol";

/// @dev Minimal standard ERC-20 (bool-returning) with an open mint.
contract MockSplv {
    string public name = "Splendor Vesting";
    string public symbol = "SPLV";
    uint8 public constant decimals = 18;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    function mint(address to, uint256 amount) external {
        totalSupply += amount;
        balanceOf[to] += amount;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        return true;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        _move(msg.sender, to, amount);
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) public virtual returns (bool) {
        _spendAllowance(from, amount);
        _move(from, to, amount);
        return true;
    }

    function _spendAllowance(address from, uint256 amount) internal {
        uint256 current = allowance[from][msg.sender];
        require(current >= amount, "allowance");
        if (current != type(uint256).max) {
            allowance[from][msg.sender] = current - amount;
        }
    }

    function _move(address from, address to, uint256 amount) internal {
        require(balanceOf[from] >= amount, "balance");
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
    }
}

/// @dev SPLV variant that burns a 1% fee on transferFrom, delivering less
///      than requested to the destination.
contract FeeSplv is MockSplv {
    function transferFrom(address from, address to, uint256 amount) public override returns (bool) {
        _spendAllowance(from, amount);
        uint256 fee = amount / 100;
        _move(from, to, amount - fee);
        balanceOf[from] -= fee;
        totalSupply -= fee;
        return true;
    }
}

/// @dev USDT-style token: transferFrom moves balances but returns nothing.
contract NoReturnSplv {
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
    }

    function approve(address spender, uint256 amount) external {
        allowance[msg.sender][spender] = amount;
    }

    function transferFrom(address from, address to, uint256 amount) external {
        uint256 current = allowance[from][msg.sender];
        require(current >= amount, "allowance");
        allowance[from][msg.sender] = current - amount;
        require(balanceOf[from] >= amount, "balance");
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
    }
}

/// @dev Broken token: transferFrom moves balances yet returns false.
contract FalseReturnSplv is MockSplv {
    function transferFrom(address from, address to, uint256 amount) public override returns (bool) {
        _spendAllowance(from, amount);
        _move(from, to, amount);
        return false;
    }
}

/// @dev Malicious token: reenters both burn paths from inside transferFrom,
///      records the revert selectors, then completes the transfer normally so
///      only the reentrancy guard stands between it and a double event.
contract ReentrantSplv is MockSplv {
    QuantumMigrationBurn public target;
    bool public attempted;
    bool public spldReentrySucceeded;
    bool public splvReentrySucceeded;
    bytes4 public spldReentryError;
    bytes4 public splvReentryError;

    function setTarget(QuantumMigrationBurn target_) external {
        target = target_;
    }

    receive() external payable {}

    function transferFrom(address from, address to, uint256 amount) public override returns (bool) {
        if (address(target) != address(0) && !attempted) {
            attempted = true;
            try target.burnSpld{value: 1 ether}(bytes32(uint256(1))) {
                spldReentrySucceeded = true;
            } catch (bytes memory err) {
                spldReentryError = bytes4(err);
            }
            try target.burnSplv(1 ether, bytes32(uint256(1))) {
                splvReentrySucceeded = true;
            } catch (bytes memory err) {
                splvReentryError = bytes4(err);
            }
        }
        return super.transferFrom(from, to, amount);
    }
}

contract QuantumMigrationBurnTest is Test {
    MockSplv public splv;
    QuantumMigrationBurn public burn;

    address ownerAddr = makeAddr("owner");
    address pauserAddr = makeAddr("pauser");
    address user = makeAddr("user");
    address other = makeAddr("other");

    bytes32 constant RECIPIENT = keccak256("SQ:test-recipient");
    address constant BURN_SINK = 0x000000000000000000000000000000000000dEaD;
    uint256 constant MIN_BURN = 1e18;
    uint256 constant MAX_BURN = 100 ether;

    event Burned(
        uint256 indexed id, address indexed sender, uint8 indexed asset, uint256 amount, bytes32 quantumRecipient
    );
    event Paused(address indexed account);
    event Unpaused(address indexed account);
    event PauserUpdated(address indexed previousPauser, address indexed newPauser);
    event OwnershipTransferStarted(address indexed previousOwner, address indexed newOwner);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    function setUp() public {
        splv = new MockSplv();
        vm.prank(ownerAddr);
        burn = new QuantumMigrationBurn(address(splv), pauserAddr, MAX_BURN);

        splv.mint(user, 1_000 ether);
        vm.deal(user, 1_000 ether);
    }

    function _approve(address account, uint256 amount) internal {
        vm.prank(account);
        splv.approve(address(burn), amount);
    }

    // ------------------------------------------------------------------
    // Construction
    // ------------------------------------------------------------------

    function test_ConstructorState() public view {
        assertEq(address(burn.splv()), address(splv));
        assertEq(burn.owner(), ownerAddr);
        assertEq(burn.pauser(), pauserAddr);
        assertEq(burn.pendingOwner(), address(0));
        assertEq(burn.MIN_BURN(), 1e18);
        assertEq(burn.maxBurn(), MAX_BURN);
        assertEq(burn.BURN_SINK(), BURN_SINK);
        assertEq(burn.ASSET_SPLD(), 0);
        assertEq(burn.ASSET_SPLV(), 1);
        assertEq(burn.burnCount(), 0);
        assertFalse(burn.paused());
    }

    function test_ConstructorRejectsInvalidInputs() public {
        vm.expectRevert(QuantumMigrationBurn.ZeroAddress.selector);
        new QuantumMigrationBurn(address(0), pauserAddr, MAX_BURN);

        vm.expectRevert(QuantumMigrationBurn.ZeroAddress.selector);
        new QuantumMigrationBurn(address(splv), address(0), MAX_BURN);

        vm.expectRevert(QuantumMigrationBurn.SplvNotContract.selector);
        new QuantumMigrationBurn(other, pauserAddr, MAX_BURN);

        // maxBurn below the minimum burn is nonsensical and refused.
        vm.expectRevert(QuantumMigrationBurn.InvalidMaxBurn.selector);
        new QuantumMigrationBurn(address(splv), pauserAddr, MIN_BURN - 1);

        // Exactly MIN_BURN is the smallest legal ceiling.
        QuantumMigrationBurn tight = new QuantumMigrationBurn(address(splv), pauserAddr, MIN_BURN);
        assertEq(tight.maxBurn(), MIN_BURN);
    }

    // ------------------------------------------------------------------
    // burnSpld
    // ------------------------------------------------------------------

    function test_BurnSpldForwardsFullValueToSink() public {
        uint256 sinkBefore = BURN_SINK.balance;

        vm.prank(user);
        burn.burnSpld{value: 5 ether}(RECIPIENT);

        assertEq(BURN_SINK.balance - sinkBefore, 5 ether);
        assertEq(address(burn).balance, 0);
        assertEq(user.balance, 995 ether);
        assertEq(burn.burnCount(), 1);
    }

    function test_BurnSpldEmitsExactEvent() public {
        vm.expectEmit(true, true, true, true, address(burn));
        emit Burned(1, user, 0, 5 ether, RECIPIENT);
        vm.prank(user);
        burn.burnSpld{value: 5 ether}(RECIPIENT);
    }

    function test_BurnSpldEnforcesMinBurn() public {
        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.BelowMinimumBurn.selector);
        burn.burnSpld{value: MIN_BURN - 1}(RECIPIENT);

        // Exactly the minimum is accepted.
        vm.prank(user);
        burn.burnSpld{value: MIN_BURN}(RECIPIENT);
        assertEq(burn.burnCount(), 1);
    }

    function test_BurnSpldRejectsZeroRecipient() public {
        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.ZeroQuantumRecipient.selector);
        burn.burnSpld{value: 1 ether}(bytes32(0));
    }

    function test_BurnSpldEnforcesMaxBurn() public {
        vm.deal(user, MAX_BURN + 1);
        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.AboveMaximumBurn.selector);
        burn.burnSpld{value: MAX_BURN + 1}(RECIPIENT);

        // Exactly the maximum is accepted.
        vm.prank(user);
        burn.burnSpld{value: MAX_BURN}(RECIPIENT);
        assertEq(burn.burnCount(), 1);
    }

    // ------------------------------------------------------------------
    // burnSplv
    // ------------------------------------------------------------------

    function test_BurnSplvMovesTokensToSink() public {
        _approve(user, 10 ether);

        vm.prank(user);
        burn.burnSplv(10 ether, RECIPIENT);

        assertEq(splv.balanceOf(BURN_SINK), 10 ether);
        assertEq(splv.balanceOf(user), 990 ether);
        assertEq(splv.balanceOf(address(burn)), 0);
        assertEq(burn.burnCount(), 1);
    }

    function test_BurnSplvEmitsExactEvent() public {
        _approve(user, 10 ether);

        vm.expectEmit(true, true, true, true, address(burn));
        emit Burned(1, user, 1, 10 ether, RECIPIENT);
        vm.prank(user);
        burn.burnSplv(10 ether, RECIPIENT);
    }

    function test_BurnSplvEnforcesMinBurn() public {
        _approve(user, type(uint256).max);

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.BelowMinimumBurn.selector);
        burn.burnSplv(MIN_BURN - 1, RECIPIENT);

        // Exactly the minimum is accepted.
        vm.prank(user);
        burn.burnSplv(MIN_BURN, RECIPIENT);
        assertEq(burn.burnCount(), 1);
    }

    function test_BurnSplvRejectsZeroRecipient() public {
        _approve(user, 1 ether);
        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.ZeroQuantumRecipient.selector);
        burn.burnSplv(1 ether, bytes32(0));
    }

    function test_BurnSplvRejectsMissingAllowance() public {
        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.SplvTransferFailed.selector);
        burn.burnSplv(1 ether, RECIPIENT);
    }

    function test_BurnSplvEnforcesMaxBurn() public {
        splv.mint(user, MAX_BURN + 1);
        _approve(user, type(uint256).max);

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.AboveMaximumBurn.selector);
        burn.burnSplv(MAX_BURN + 1, RECIPIENT);

        // Exactly the maximum is accepted.
        vm.prank(user);
        burn.burnSplv(MAX_BURN, RECIPIENT);
        assertEq(burn.burnCount(), 1);
    }

    // ------------------------------------------------------------------
    // Shared burn id counter
    // ------------------------------------------------------------------

    function test_IdIncrementsAcrossMixedBurns() public {
        _approve(user, type(uint256).max);

        vm.expectEmit(true, true, true, true, address(burn));
        emit Burned(1, user, 0, 2 ether, RECIPIENT);
        vm.prank(user);
        burn.burnSpld{value: 2 ether}(RECIPIENT);

        vm.expectEmit(true, true, true, true, address(burn));
        emit Burned(2, user, 1, 3 ether, RECIPIENT);
        vm.prank(user);
        burn.burnSplv(3 ether, RECIPIENT);

        vm.expectEmit(true, true, true, true, address(burn));
        emit Burned(3, user, 0, 4 ether, RECIPIENT);
        vm.prank(user);
        burn.burnSpld{value: 4 ether}(RECIPIENT);

        assertEq(burn.burnCount(), 3);
    }

    // ------------------------------------------------------------------
    // Non-standard tokens
    // ------------------------------------------------------------------

    function test_FeeOnTransferSplvRefused() public {
        FeeSplv feeToken = new FeeSplv();
        vm.prank(ownerAddr);
        QuantumMigrationBurn feeBurn = new QuantumMigrationBurn(address(feeToken), pauserAddr, MAX_BURN);

        feeToken.mint(user, 100 ether);
        vm.prank(user);
        feeToken.approve(address(feeBurn), type(uint256).max);

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.UnexpectedSplvAmount.selector);
        feeBurn.burnSplv(50 ether, RECIPIENT);

        assertEq(feeBurn.burnCount(), 0);
    }

    function test_NoReturnSplvAccepted() public {
        NoReturnSplv usdtStyle = new NoReturnSplv();
        vm.prank(ownerAddr);
        QuantumMigrationBurn nrBurn = new QuantumMigrationBurn(address(usdtStyle), pauserAddr, MAX_BURN);

        usdtStyle.mint(user, 100 ether);
        vm.prank(user);
        usdtStyle.approve(address(nrBurn), 100 ether);

        vm.expectEmit(true, true, true, true, address(nrBurn));
        emit Burned(1, user, 1, 25 ether, RECIPIENT);
        vm.prank(user);
        nrBurn.burnSplv(25 ether, RECIPIENT);

        assertEq(usdtStyle.balanceOf(BURN_SINK), 25 ether);
        assertEq(usdtStyle.balanceOf(user), 75 ether);
        assertEq(nrBurn.burnCount(), 1);
    }

    function test_FalseReturnSplvRefused() public {
        FalseReturnSplv falseToken = new FalseReturnSplv();
        vm.prank(ownerAddr);
        QuantumMigrationBurn frBurn = new QuantumMigrationBurn(address(falseToken), pauserAddr, MAX_BURN);

        falseToken.mint(user, 100 ether);
        vm.prank(user);
        falseToken.approve(address(frBurn), type(uint256).max);

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.SplvTransferFailed.selector);
        frBurn.burnSplv(10 ether, RECIPIENT);
    }

    // ------------------------------------------------------------------
    // Pause
    // ------------------------------------------------------------------

    function test_PauseBlocksBothBurnsAndUnpauseRestores() public {
        _approve(user, type(uint256).max);

        vm.expectEmit(true, false, false, true, address(burn));
        emit Paused(pauserAddr);
        vm.prank(pauserAddr);
        burn.pause();
        assertTrue(burn.paused());

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.EnforcedPause.selector);
        burn.burnSpld{value: 1 ether}(RECIPIENT);

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.EnforcedPause.selector);
        burn.burnSplv(1 ether, RECIPIENT);

        vm.expectEmit(true, false, false, true, address(burn));
        emit Unpaused(pauserAddr);
        vm.prank(pauserAddr);
        burn.unpause();
        assertFalse(burn.paused());

        vm.prank(user);
        burn.burnSpld{value: 1 ether}(RECIPIENT);
        vm.prank(user);
        burn.burnSplv(1 ether, RECIPIENT);
        assertEq(burn.burnCount(), 2);
    }

    function test_PauseOnlyPauser() public {
        vm.prank(ownerAddr);
        vm.expectRevert(QuantumMigrationBurn.NotPauser.selector);
        burn.pause();

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.NotPauser.selector);
        burn.pause();

        vm.prank(pauserAddr);
        burn.pause();

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.NotPauser.selector);
        burn.unpause();
    }

    function test_PauseStateTransitionsGuarded() public {
        vm.prank(pauserAddr);
        vm.expectRevert(QuantumMigrationBurn.ExpectedPause.selector);
        burn.unpause();

        vm.prank(pauserAddr);
        burn.pause();

        vm.prank(pauserAddr);
        vm.expectRevert(QuantumMigrationBurn.EnforcedPause.selector);
        burn.pause();
    }

    // ------------------------------------------------------------------
    // Direct sends
    // ------------------------------------------------------------------

    function test_DirectSendsRevert() public {
        vm.deal(other, 3 ether);

        // Plain value transfer hits receive().
        vm.prank(other);
        (bool ok, bytes memory ret) = address(burn).call{value: 1 ether}("");
        assertFalse(ok);
        assertEq(bytes4(ret), QuantumMigrationBurn.DirectTransferRejected.selector);

        // Value with unknown calldata hits fallback().
        vm.prank(other);
        (ok, ret) = address(burn).call{value: 1 ether}(hex"12345678");
        assertFalse(ok);
        assertEq(bytes4(ret), QuantumMigrationBurn.DirectTransferRejected.selector);

        // Unknown calldata without value also hits fallback().
        vm.prank(other);
        (ok, ret) = address(burn).call(hex"12345678");
        assertFalse(ok);
        assertEq(bytes4(ret), QuantumMigrationBurn.DirectTransferRejected.selector);

        assertEq(address(burn).balance, 0);
        assertEq(other.balance, 3 ether);
    }

    // ------------------------------------------------------------------
    // Reentrancy
    // ------------------------------------------------------------------

    function test_ReentrancyViaMaliciousTokenBlocked() public {
        ReentrantSplv evil = new ReentrantSplv();
        vm.prank(ownerAddr);
        QuantumMigrationBurn evilBurn = new QuantumMigrationBurn(address(evil), pauserAddr, MAX_BURN);
        evil.setTarget(evilBurn);
        vm.deal(address(evil), 1 ether);

        evil.mint(user, 100 ether);
        vm.prank(user);
        evil.approve(address(evilBurn), type(uint256).max);

        // The outer burn completes; both reentrant attempts from inside
        // transferFrom hit the shared guard.
        vm.expectEmit(true, true, true, true, address(evilBurn));
        emit Burned(1, user, 1, 10 ether, RECIPIENT);
        vm.prank(user);
        evilBurn.burnSplv(10 ether, RECIPIENT);

        assertTrue(evil.attempted());
        assertFalse(evil.spldReentrySucceeded());
        assertFalse(evil.splvReentrySucceeded());
        assertEq(evil.spldReentryError(), QuantumMigrationBurn.ReentrantCall.selector);
        assertEq(evil.splvReentryError(), QuantumMigrationBurn.ReentrantCall.selector);

        // Exactly one burn was recorded and the guard reset afterwards.
        assertEq(evilBurn.burnCount(), 1);
        vm.prank(user);
        evilBurn.burnSplv(1 ether, RECIPIENT);
        assertEq(evilBurn.burnCount(), 2);
    }

    // ------------------------------------------------------------------
    // Roles
    // ------------------------------------------------------------------

    function test_SetPauserOnlyOwnerAndNonZero() public {
        vm.prank(other);
        vm.expectRevert(QuantumMigrationBurn.NotOwner.selector);
        burn.setPauser(other);

        vm.prank(pauserAddr);
        vm.expectRevert(QuantumMigrationBurn.NotOwner.selector);
        burn.setPauser(other);

        vm.prank(ownerAddr);
        vm.expectRevert(QuantumMigrationBurn.ZeroAddress.selector);
        burn.setPauser(address(0));

        vm.expectEmit(true, true, false, true, address(burn));
        emit PauserUpdated(pauserAddr, other);
        vm.prank(ownerAddr);
        burn.setPauser(other);
        assertEq(burn.pauser(), other);

        vm.prank(other);
        burn.pause();
        assertTrue(burn.paused());

        vm.prank(pauserAddr);
        vm.expectRevert(QuantumMigrationBurn.NotPauser.selector);
        burn.unpause();
    }

    function test_TwoStepOwnershipTransfer() public {
        vm.prank(other);
        vm.expectRevert(QuantumMigrationBurn.NotOwner.selector);
        burn.transferOwnership(other);

        vm.expectEmit(true, true, false, true, address(burn));
        emit OwnershipTransferStarted(ownerAddr, other);
        vm.prank(ownerAddr);
        burn.transferOwnership(other);
        assertEq(burn.owner(), ownerAddr);
        assertEq(burn.pendingOwner(), other);

        vm.prank(user);
        vm.expectRevert(QuantumMigrationBurn.NotPendingOwner.selector);
        burn.acceptOwnership();

        vm.expectEmit(true, true, false, true, address(burn));
        emit OwnershipTransferred(ownerAddr, other);
        vm.prank(other);
        burn.acceptOwnership();
        assertEq(burn.owner(), other);
        assertEq(burn.pendingOwner(), address(0));

        // Old owner has lost control; the new owner has it.
        vm.prank(ownerAddr);
        vm.expectRevert(QuantumMigrationBurn.NotOwner.selector);
        burn.setPauser(ownerAddr);

        vm.prank(other);
        burn.setPauser(other);
        assertEq(burn.pauser(), other);
    }

    function test_OwnershipTransferCanBeCancelled() public {
        vm.prank(ownerAddr);
        burn.transferOwnership(other);

        vm.prank(ownerAddr);
        burn.transferOwnership(address(0));
        assertEq(burn.pendingOwner(), address(0));

        vm.prank(other);
        vm.expectRevert(QuantumMigrationBurn.NotPendingOwner.selector);
        burn.acceptOwnership();
    }

    // ------------------------------------------------------------------
    // Custody-free invariant
    // ------------------------------------------------------------------

    function test_ContractNeverHoldsFunds() public {
        _approve(user, type(uint256).max);

        vm.startPrank(user);
        burn.burnSpld{value: 7 ether}(RECIPIENT);
        burn.burnSplv(11 ether, RECIPIENT);
        burn.burnSpld{value: 1 ether}(RECIPIENT);
        vm.stopPrank();

        assertEq(address(burn).balance, 0);
        assertEq(splv.balanceOf(address(burn)), 0);
        assertEq(BURN_SINK.balance, 8 ether);
        assertEq(splv.balanceOf(BURN_SINK), 11 ether);
    }
}
