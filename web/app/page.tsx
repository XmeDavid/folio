export default function HomePage() {
  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col gap-6 px-6 py-16">
      <header className="flex flex-col gap-2">
        <div className="text-xs uppercase tracking-widest text-[--color-muted]">
          Folio
        </div>
        <h1 className="text-3xl font-semibold tracking-tight">
          Personal finance, without the noise.
        </h1>
        <p className="text-[--color-muted]">
          Scaffold is up. Next step: wire the backend API and build the onboarding flow.
        </p>
      </header>

      <section className="grid gap-3 sm:grid-cols-3">
        <Card title="GoCardless" body="Revolut + EU banks, read-only." />
        <Card title="IBKR Flex" body="Portfolio activity via token." />
        <Card title="Imports" body="camt.053 / CSV / manual." />
      </section>

      <footer className="mt-auto pt-12 text-xs text-[--color-muted]">
        v0.0.0-dev
      </footer>
    </main>
  );
}

function Card({ title, body }: { title: string; body: string }) {
  return (
    <div className="rounded-lg border border-[--color-border] bg-[--color-surface] p-4">
      <div className="text-sm font-medium">{title}</div>
      <div className="mt-1 text-sm text-[--color-muted]">{body}</div>
    </div>
  );
}
