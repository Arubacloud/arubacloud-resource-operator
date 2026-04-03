#!/bin/bash

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

print_status() {
    echo -e "${GREEN}[TEST]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WAIT]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_status "🚀 Starting ArubaProject workflow test..."

# Apply the test project
print_status "🏗️  Creating test ArubaProject..."
kubectl apply -f config/samples/arubacloud.com_v1alpha1_arubaproject.yaml

sleep 5

# Check project status
print_status "📊 Project status:"
kubectl get arubaproject test-project -o wide

# Test project update
print_status "🔄 Testing project update..."
kubectl patch arubaproject test-project --type='merge' -p="{\"spec\":{\"description\":\"Updated description\",\"tags\":[\"test\",\"updated\",\"another-tag\"]}}"

sleep 5

# Check project status
print_status "📊 Project status:"
kubectl get arubaproject test-project -o wide

# Test deletion phase
print_status "🗑️  Testing project deletion..."
kubectl delete arubaproject test-project

sleep 5

# Check project status
print_status "📊 Project status:"
kubectl get arubaproject test-project -o wide

print_status "✅ Test completed! Check the logs for detailed information."

# Optional: Show recent events
print_info "📝 Recent events:"
kubectl get events --sort-by=.metadata.creationTimestamp | tail -10
