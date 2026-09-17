# Native token standards

Anyone can deploy these. That is the point. The chain’s **own** rules are
what a deployed collection or token must obey. A QSol contract someone
writes later is their code; these modules are ours.

| Standard | What it is | Who may mint |
| --- | --- | --- |
| **QRC-20** | Fungible token (routed) | Collection **owner** (the deployer), unless they transfer ownership |
| **QRC-721** | NFT (routed, `qrc721_v1`) | Collection **owner** only |
| **QRC-1155** | Multi-token / editions (routed, `qrc1155_v1`) | Collection **owner** only |
| **QRC-4626** | Vault shares — **not routed** | Not a live native module in this revision |

Deploy is open. Mint on *someone else’s* contract is not. That is the same
rule ERC-20 / ERC-721 collections use: Alice’s “Cool Cats” stays Alice’s
supply. Bob deploys his own collection if he wants to mint.

Native **SPLD** is not a QRC-20. It is minted once at genesis (26 billion)
and never again. QRC minting creates a *different* token inside that
contract, not extra SPLD.

Routed forks: `qrc20_v2`, `qrc721_v1`, `qrc1155_v1` (each needs
`module_revert_v1` when the action is post-launch). `qrc4626_v1` may appear
in a genesis document; it does not make the vault module reachable.
