#!/bin/bash

set -e

echo "🚀 Starting local ArubaProject controller testing..."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    print_error "kubectl is not installed or not in PATH"
    exit 1
fi

# Check if kind or minikube is running
if ! kubectl cluster-info &> /dev/null; then
    print_error "No Kubernetes cluster found. Please start kind, minikube, or connect to a cluster"
    exit 1
fi

print_status "Kubernetes cluster detected"

# Generate CRDs
print_status "Generating CRDs..."
make manifests

# Install CRDs
print_status "Installing CRDs..."
make install

# Create namespace if it doesn't exist
kubectl create namespace aruba-operator-system --dry-run=client -o yaml | kubectl apply -f -

# Apply secrets (you need to edit these with real values)
print_warning "Make sure to update config/samples/aruba-config-configmap.yaml with real configurations"
kubectl apply -f config/samples/aruba-config-configmap.yaml

# Apply secrets (you need to edit these with real values)
print_warning "Make sure to update config/samples/aruba-config-secret.yaml with real secrets"
kubectl apply -f config/samples/aruba-config-secret.yaml

# Build and run the controller
print_status "Building controller..."
make build

print_status "Starting controller in development mode..."
print_warning "Press Ctrl+C to stop the controller"

# Run controller with verbose logging
KUBECONFIG=$HOME/.kube/config make run ARGS="--log-level=debug"
