#!/bin/sh
# First certificate for variant A (bundled nginx). Not needed with an own proxy or tunnel.
# Prerequisite: .env with DOMAIN and LETSENCRYPT_EMAIL, DNS points to this host, port 80/443 open.
set -eu
cd "$(dirname "$0")/.."

[ -f .env ] || { echo ".env is missing" >&2; exit 1; }
set -a; . ./.env; set +a
: "${DOMAIN:?set DOMAIN in .env}"
: "${LETSENCRYPT_EMAIL:?set LETSENCRYPT_EMAIL in .env}"

DC="docker compose --profile proxy"
CERT="/etc/letsencrypt/live/$DOMAIN"

echo "Creating placeholder certificate so nginx can start ..."
$DC run --rm --entrypoint sh certbot -c "
  mkdir -p $CERT &&
  openssl req -x509 -nodes -newkey rsa:2048 -days 1 \
    -keyout $CERT/privkey.pem -out $CERT/fullchain.pem -subj /CN=localhost"

echo "Starting nginx ..."
$DC up -d --no-deps nginx

echo "Removing placeholder and requesting real certificate ..."
$DC run --rm --entrypoint sh certbot -c "
  rm -rf /etc/letsencrypt/live/$DOMAIN /etc/letsencrypt/archive/$DOMAIN /etc/letsencrypt/renewal/$DOMAIN.conf"
$DC run --rm --entrypoint certbot certbot certonly --webroot -w /var/www/certbot \
  -d "$DOMAIN" --email "$LETSENCRYPT_EMAIL" --agree-tos --no-eff-email --non-interactive

echo "Reloading nginx and starting everything ..."
$DC exec nginx nginx -s reload
$DC up -d
echo "Done: https://$DOMAIN"
