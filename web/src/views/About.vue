<template>
  <div class="min-h-screen bg-bg flex flex-col">
    <!-- Header mirrors the Landing page. -->
    <header class="border-b border-border bg-bg-card">
      <div class="max-w-5xl mx-auto px-6 h-16 flex items-center justify-between">
        <router-link to="/" class="flex items-center gap-2">
          <img src="/logo.svg" class="w-7 h-7" alt="" />
          <span class="text-lg font-semibold text-fg-strong">skoed</span>
          <span class="text-xs text-fg-muted hidden sm:inline">&middot; self-hosted DNS filtering</span>
        </router-link>
        <router-link to="/login" class="btn-primary">
          Login
        </router-link>
      </div>
    </header>

    <main class="flex-1">
      <article class="max-w-3xl mx-auto px-6 py-12 space-y-10">
        <header class="space-y-3">
          <h1 class="text-3xl sm:text-4xl font-bold text-fg-strong">About skoed</h1>
          <p class="text-lg text-fg-muted">
            Self-hosted DNS filtering with multi-node sync.
          </p>
        </header>

        <!-- Name -->
        <section class="card p-6 space-y-3">
          <h2 class="text-lg font-semibold text-fg-strong flex items-center gap-2">
            <span class="text-accent">🛡</span> The name
          </h2>
          <p class="text-fg">
            <strong class="font-mono text-accent">skoed</strong>
            <span class="text-fg-muted">/skoˈeːd/</span> &mdash;
            <em>shield</em>, in
            <a href="https://en.wikipedia.org/wiki/Breton_language"
               class="text-accent hover:underline"
               target="_blank" rel="noopener">Breton</a>
            (Brezhoneg), the Celtic language of Brittany.
          </p>
          <p class="text-sm text-fg-muted">
            Picked because that's what the project does: stand between your
            network and the noisy parts of the internet without making a
            fuss about it.
          </p>
        </section>

        <!-- What -->
        <section class="card p-6 space-y-3">
          <h2 class="text-lg font-semibold text-fg-strong">What it does</h2>
          <ul class="space-y-2 text-fg">
            <li>
              <strong class="text-fg-strong">Filters DNS</strong> at the
              network edge &mdash; plain DNS, DoH (RFC 8484), DoT (RFC 7858).
            </li>
            <li>
              <strong class="text-fg-strong">Syncs across multiple nodes</strong>
              via Raft consensus, so your household / small office / homelab
              keeps filtering when any single node dies.
            </li>
            <li>
              <strong class="text-fg-strong">Profiles &amp; schedules</strong>
              for per-client policy: kid-mode 19:00&ndash;07:00, separate
              rules for guests, etc.
            </li>
            <li>
              <strong class="text-fg-strong">Quietly observable</strong>:
              Prometheus <code>/metrics</code>, replicated audit log,
              one-binary install, no SaaS account.
            </li>
          </ul>
        </section>

        <!-- What it's not -->
        <section class="card p-6 space-y-3">
          <h2 class="text-lg font-semibold text-fg-strong">What it isn't</h2>
          <ul class="space-y-2 text-fg-muted text-sm">
            <li>
              <strong class="text-fg">Not a DHCP server.</strong> skoed
              <em>reads</em> leases from Kea / dnsmasq / generic HTTP-JSON
              sources to enrich the query log; serving leases is a
              permanent non-goal.
            </li>
            <li>
              <strong class="text-fg">Not a captive-portal proxy.</strong>
              DNS filtering is the hammer; clients with hard-coded
              resolver IPs need a firewall rule (skoed surfaces DoH
              probes so you know which clients to chase).
            </li>
            <li>
              <strong class="text-fg">Not a SaaS.</strong> The public
              landing page is the <em>only</em> unauthenticated surface,
              and it's there to let you sanity-check a blocklist URL
              before you install. Everything else is admin-gated.
            </li>
          </ul>
        </section>

        <!-- Where to next -->
        <section class="card p-6 space-y-3">
          <h2 class="text-lg font-semibold text-fg-strong">Get started</h2>
          <ul class="space-y-2 text-fg">
            <li>
              <a href="https://github.com/skoed/skoed"
                 class="text-accent hover:underline"
                 target="_blank" rel="noopener">Source on GitHub</a>
            </li>
            <li>
              <a href="https://docs.skoed.io"
                 class="text-accent hover:underline"
                 target="_blank" rel="noopener">Documentation</a>
              &mdash; install, cluster bootstrap, configuration reference.
            </li>
            <li>
              <router-link to="/login" class="text-accent hover:underline">
                Admin login
              </router-link>
              &mdash; if this node is yours.
            </li>
          </ul>
        </section>

        <p class="text-xs text-fg-subtle pt-4">
          skoed is MIT-licensed. Brittany-flag-coloured ASCII not included.
        </p>
      </article>
    </main>

    <footer class="px-6 py-4 border-t border-border flex flex-wrap items-center justify-between text-xs text-fg-muted">
      <span>skoed &mdash; self-hosted DNS filtering &middot; v1</span>
      <span class="flex gap-4">
        <router-link to="/" class="hover:text-accent">home</router-link>
        <router-link to="/about" class="hover:text-accent">about</router-link>
        <a href="https://docs.skoed.io" class="hover:text-accent" target="_blank" rel="noopener">docs</a>
        <a href="https://github.com/skoed/skoed" class="hover:text-accent" target="_blank" rel="noopener">github</a>
      </span>
    </footer>
  </div>
</template>

<script setup lang="ts">
// Static page; no script needed.
</script>
