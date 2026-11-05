# Domains and HTTPS

**English** · [Bahasa Indonesia](domains.id.md)

How applications get an address: shared domain hosting, custom domains, and the Cloudflare options. For the admin steps (DNS records, Let's Encrypt limits) see also [Installing on Ubuntu](install-ubuntu.md#domain-hosting-for-users).

## Shared domain hosting

Admins open the **Server → Domain hosting** page and register a wildcard domain, for example `porto.devkriyanesia.my.id`, then create a single `A` record `*.porto` pointing to the server IP (**DNS only** proxy). Runova checks its DNS and displays the result. Afterward, every user—admin or regular—selects that domain in wizard step 4 or the Domain tab, types a name (`cv-budi`), and the application is immediately available at `cv-budi.porto.devkriyanesia.my.id`.

- Subdomains per user are bounded by their application quota; the limit per application remains 5 addresses.
- Names are 3–40 characters (lowercase letters, numbers, hyphens); `www`, `api`, `mail`, `admin`, `runova`, panel labels, and additional admin-reserved names cannot be used. First come, first served.
- Disabling domain hosting revokes all its subdomains from Caddy and notifies their owners; a domain can only be deleted when no subdomains are using it.
- One Let's Encrypt certificate is issued per subdomain. Let's Encrypt limits new certificates to 50 per week per registered domain; the Domain hosting card displays an estimate and warns when approaching the limit. Behind nginx (trusted proxy set) Caddy takes no Let's Encrypt certificates and the estimate is not shown.
- Custom domains (e.g. `budi.com`) can only be added by admins; existing custom domains owned by regular users continue running.
- Cloudflare Proxy (orange cloud) and Cloudflare Tunnel are not supported for second-level subdomains.

## Connecting a custom domain (admin)

When a domain is entered, Runova displays a setup guide prefilled with the subdomain, domain, and server address. There are three methods, depending on network conditions:

| Method | When to use | What to create in Cloudflare |
|---|---|---|
| **Direct DNS** | Server has a public IP, ports 80 and 443 are open | `A` record to server public IP, **DNS only** proxy |
| **Cloudflare Proxy** | Hide server IP and gain Cloudflare protection | Same `A` record. After HTTPS is active in Runova, switch to **Proxied** and set SSL/TLS to **Full (strict)** |
| **Cloudflare Tunnel** | Server without a public IP or without open ports | Networking → Tunnels → Routes → Published application, Service URL `http://localhost:80` (Caddy on the Runova server) |

For Direct DNS and Proxy, certificates are issued by Caddy on the server. For Tunnel, HTTPS is handled by Cloudflare.

A Runova server with only a local IP behind another server running nginx is covered by [nginx in front of Runova](nginx-front-proxy.md), including wildcard certificates for the domain hosting. With a trusted proxy set, Caddy issues every certificate from its internal CA (nginx holds the public ones), so new subdomains work at once and nginx may redirect port 80 itself.
