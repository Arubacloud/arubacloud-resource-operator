#!/bin/bash
# Usage: NN=9 ACTION=apply ./test_runner.sh

set -hex
NN=${NN:-1}
ACTION=${ACTION:-apply}
TENANT=${TENANT:-ARU-329997}
NAME=${NAME:-aruba-resource}
NAMESPACE=${NAMESPACE:-default}
QNT=${QNT:-00}

SAMPLES_DIR="../../config/samples"
FIXTURES_DIR="./fixtures"

# Run kubectl command for each file in selected test set
for i in $(cat "$FIXTURES_DIR/Test${NN}_${QNT}" || cat "$FIXTURES_DIR/Test${NN}_00" ); do 
  TMPFILE=$(mktemp)
  sed -e "s/__TENANT__/$TENANT/g" -e "s/__NAME__/$NAME/g" -e "s/__NAMESPACE__/$NAMESPACE/g" "$SAMPLES_DIR/${i}" > "$TMPFILE"
  kubectl $ACTION -f "$TMPFILE" &
done

wait
