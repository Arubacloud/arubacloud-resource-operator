#!/bin/bash
# Usage: NN=9 ACTION=apply ./test_runner.sh
# Usage: ./test_runner.sh -n=9 -a=apply -r aruba-cloud-example -t ARU-12345

set -e
NN=${NN:-1}
ACTION=${ACTION:-apply}
TENANT=${TENANT:-ARU-329997}
NAME=${NAME:-aruba-resource}


while [[ $# -gt 0 ]]; do
    case $1 in
        --number|-n) NN="$2"; shift 2 ;;
        --action|-a) ACTION="$2"; shift 2 ;;
        --tenant|-t) TENANT="$2"; shift 2 ;;
        --resource-name|-r) NAME="$2"; shift 2 ;;
        *) echo "Unknown argument $1"; exit 1 ;;
    esac
done

SAMPLES_DIR="../../config/samples"
FIXTURES_DIR="./fixtures"

# Run kubectl command for each file in selected test set
for i in $(cat "$FIXTURES_DIR/Test${NN}"); do
  TMP_FILE=$(mktemp)
  sed -e "s/__TENANT__/$TENANT/g" -e "s/__NAME__/$NAME/g" "$SAMPLES_DIR/${i}" > "$TMP_FILE"
  kubectl $ACTION -f "$TMP_FILE" &
done

wait
