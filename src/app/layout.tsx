import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import Link from "next/link";
import {
  LayoutDashboard,
  ArrowLeftRight,
  Upload,
  Layers,
  Landmark,
  CreditCard,
  Wallet,
  Building2,
} from "lucide-react";
import { Providers } from "@/components/providers";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Folio -- Portfolio Tracker",
  description: "Personal finance tracking, portfolio management, and spending analysis",
};

const navItems = [
  { href: "/overview", label: "Overview", icon: Landmark },
  { href: "/", label: "Investments", icon: LayoutDashboard },
  { href: "/spending", label: "Spending", icon: CreditCard },
  { href: "/banking", label: "Banking", icon: Wallet },
  { href: "/positions", label: "Positions", icon: Layers },
  { href: "/transactions", label: "Trades", icon: ArrowLeftRight },
  { href: "/import", label: "Import", icon: Upload },
  { href: "/accounts", label: "Accounts", icon: Building2 },
];

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className="dark">
      <body
        className={`${geistSans.variable} ${geistMono.variable} font-sans antialiased`}
      >
        <div className="flex min-h-screen">
          <aside className="w-56 shrink-0 border-r border-border-subtle bg-bg-secondary flex flex-col">
            <div className="p-5 border-b border-border-subtle">
              <h1 className="text-lg font-semibold tracking-tight text-text-primary">
                folio<span className="text-accent">.</span>
              </h1>
              <p className="text-[11px] text-text-tertiary mt-0.5 font-mono tracking-wider uppercase">
                Finance Tracker
              </p>
            </div>
            <nav className="flex-1 p-3 space-y-0.5">
              {navItems.map((item) => (
                <Link
                  key={item.href}
                  href={item.href}
                  className="flex items-center gap-2.5 px-3 py-2 text-sm text-text-secondary rounded-lg hover:bg-bg-hover hover:text-text-primary transition-colors"
                >
                  <item.icon size={16} className="opacity-60" />
                  {item.label}
                </Link>
              ))}
            </nav>
            <div className="p-4 border-t border-border-subtle">
              <p className="text-[10px] text-text-tertiary font-mono">
                v2.0.0
              </p>
            </div>
          </aside>

          <main className="flex-1 overflow-auto">
            <div className="max-w-[1400px] mx-auto p-6">
              <Providers>{children}</Providers>
            </div>
          </main>
        </div>
      </body>
    </html>
  );
}
