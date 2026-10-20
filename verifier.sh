#!/usr/bin/env bash
#
# Verification du module whatsapp-go. Chaque etape de travail se termine par ce script.
#
# POURQUOI UN SCRIPT ET PAS DEUX COMMANDES. Un « go vet && go test » ne verifie pas la seule
# promesse que ce module fait a qui l'importe : AUCUNE dependance hors bibliotheque standard
# dans le code de production. C'est l'etape « dependances » qui la tient, et elle n'existe
# nulle part ailleurs.
#
# gofmt et govulncheck se resolvent PAR GO, jamais par le PATH : sur une installation par
# toolchain, gofmt vit dans le GOROOT et n'est pas sur le PATH. Un « bash verifier.sh » lance
# depuis PowerShell echouait sinon sur « gofmt: command not found » alors que tout etait
# installe.

set -euo pipefail

cd "$(dirname "$0")"

GOFMT="$(go env GOROOT)/bin/gofmt"
GOVULNCHECK="$(go env GOPATH)/bin/govulncheck"

echo "== gofmt =="
non_formates="$("$GOFMT" -l .)"
if [ -n "$non_formates" ]; then
	echo "fichiers non formates :"
	echo "$non_formates"
	echo "corriger avec : $GOFMT -w ."
	exit 1
fi
echo "ok"

echo "== go vet =="
go vet ./...
echo "ok"

echo "== go fix =="
# Le modernizer depuis Go 1.26. Une sortie non vide signale du code qu'il sait reecrire.
reecritures="$(go fix -diff ./... 2>/dev/null || true)"
if [ -n "$reecritures" ]; then
	echo "go fix propose des reecritures :"
	echo "$reecritures"
	exit 1
fi
echo "ok"

echo "== go test -race =="
go test -race ./...
echo "ok"

echo "== exemples =="
go test -run Example ./...
echo "ok"

echo "== dependances =="
# Aucune dependance hors bibliotheque standard dans le code de PRODUCTION. go-cmp est une
# dependance de test, et l'elagage de graphe de Go la garde hors du build du consommateur.
MODULE="$(go list -m)"
tierces="$(go list -deps ./... | grep -E '^[^/]+\.[^/]+/' | grep -v '^vendor/' | grep -v "^$MODULE" || true)"
if [ -n "$tierces" ]; then
	echo "dependances tierces dans le code de production :"
	echo "$tierces"
	exit 1
fi
echo "ok"

echo "== go mod tidy =="
go mod tidy -diff
echo "ok"

if [ -x "$GOVULNCHECK" ]; then
	echo "== govulncheck =="
	"$GOVULNCHECK" ./...
	echo "ok"
else
	echo "== govulncheck == (absent, ignore)"
fi

echo
echo "whatsapp-go : tout est vert."
