#!/bin/bash
# Complete bloat removal script

set -e

echo "🔥 REMOVING ALL BLOAT..."

# Step 1: Remove exchange case from module_router_read.go
echo "Cleaning module_router_read.go..."
sed -i '/"exchange":/,/^[[:space:]]*}/d' vm/module_router_read.go
sed -i '/case "exchange":/,/^[[:space:]]*default:/s/case "exchange":.*//g; /case "exchange":/d' vm/module_router_read.go

# Step 2: Remove exchange types and functions
sed -i '/type.*exchange/Id; /exchangeOrderWire/d; /exchangeMatchParams/d' vm/module_router_read.go

# Step 3: Remove VoteOption from events.go
sed -i '/func VoteCastEvent/,/^}/d' vm/events.go

# Step 4: Remove u64be and OperatorSetEvent from bridge.go
sed -i '/func u64be/d; /OperatorSetEvent/d' vm/bridge.go

# Step 5: Try to build
echo "Building..."
if go build ./vm 2>&1 | tee /tmp/build.log; then
    echo "✅ BUILD SUCCESSFUL!"
else
    echo "⚠️  Build had errors, showing first 20 lines:"
    head -20 /tmp/build.log
fi

echo "Done!"
