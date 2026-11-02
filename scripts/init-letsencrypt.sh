#!/bin/sh
# Erstes Zertifikat für Variante A (mitgelieferter nginx). Nicht nötig bei eigenem Proxy oder Tunnel.
# Voraussetzung: .env mit DOMAIN und LETSENCRYPT_EMAIL, DNS zeigt auf diesen Host, Port 80/443 offen.
set -eu
cd "$(dirname "$0")/.."

[ -f .env ] || { echo ".env fehlt" >&2; exit 1; }
set -a; . ./.env; set +a
: "${DOMAIN:?DOMAIN in .env setzen}"
: "${LETSENCRYPT_EMAIL:?LETSENCRYPT_EMAIL in .env setzen}"

DC="docker compose --profile proxy"
CERT="/etc/letsencrypt/live/$DOMAIN"

echo "Erzeuge Platzhalterzertifikat, damit nginx starten kann ..."
$DC run --rm --entrypoint sh certbot -c "
  mkdir -p $CERT &&
  openssl req -x509 -nodes -newkey rsa:2048 -days 1 \
    -keyout $CERT/privkey.pem -out $CERT/fullchain.pem -subj /CN=localhost"

echo "Starte nginx ..."
$DC up -d --no-deps nginx

echo "Entferne Platzhalter und fordere echtes Zertifikat an ..."
$DC run --rm --entrypoint sh certbot -c "
  rm -rf /etc/letsencrypt/live/$DOMAIN /etc/letsencrypt/archive/$DOMAIN /etc/letsencrypt/renewal/$DOMAIN.conf"
$DC run --rm --entrypoint certbot certbot certonly --webroot -w /var/www/certbot \
  -d "$DOMAIN" --email "$LETSENCRYPT_EMAIL" --agree-tos --no-eff-email --non-interactive

echo "Lade nginx neu und starte alles ..."
$DC exec nginx nginx -s reload
$DC up -d
echo "Fertig: https://$DOMAIN"
