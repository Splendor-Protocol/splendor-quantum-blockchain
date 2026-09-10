#!/bin/bash
# Fix remaining build errors quickly

# Fix module_router_read.go - remove exchange case and references
sed -i '/case "exchange":/,/^[[:space:]]*case /{ /case "exchange":/d; /return dispatchExchangeRead/d; }' vm/module_router_read.go
sed -i '/exchangeOrderWire\|exchangeMatchParams\|dispatchExchangeRead/d' vm/module_router_read.go

# Fix events.go - remove V oteOption function
sed -i '/func VoteCastEvent.*VoteOption/,/^}/d' vm/events.go

# Fix bridge.go - remove u64be and OperatorSetEvent (stub them if needed)
sed -i '/func u64be/d; /func OperatorSetEvent/d' vm/bridge.go

echo "Fixed! Testing build..."
go build ./vm 2>&1 | head -20
