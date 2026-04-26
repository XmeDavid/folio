export const CURRENCY_OPTIONS = [
  { value: "CHF", label: "CHF - Swiss franc" },
  { value: "EUR", label: "EUR - Euro" },
  { value: "USD", label: "USD - US dollar" },
  { value: "GBP", label: "GBP - British pound" },
  { value: "BRL", label: "BRL - Brazilian real" },
  { value: "PHP", label: "PHP - Philippine peso" },
  { value: "CAD", label: "CAD - Canadian dollar" },
  { value: "AUD", label: "AUD - Australian dollar" },
  { value: "JPY", label: "JPY - Japanese yen" },
  { value: "SEK", label: "SEK - Swedish krona" },
  { value: "NOK", label: "NOK - Norwegian krone" },
  { value: "DKK", label: "DKK - Danish krone" },
  { value: "PLN", label: "PLN - Polish zloty" },
  { value: "CZK", label: "CZK - Czech koruna" },
  { value: "HUF", label: "HUF - Hungarian forint" },
  { value: "RON", label: "RON - Romanian leu" },
  { value: "SGD", label: "SGD - Singapore dollar" },
  { value: "HKD", label: "HKD - Hong Kong dollar" },
  { value: "NZD", label: "NZD - New Zealand dollar" },
  { value: "MXN", label: "MXN - Mexican peso" },
  { value: "ZAR", label: "ZAR - South African rand" },
  { value: "BTC", label: "BTC - Bitcoin" },
  { value: "ETH", label: "ETH - Ethereum" },
  { value: "SOL", label: "SOL - Solana" },
  { value: "ADA", label: "ADA - Cardano" },
  { value: "XRP", label: "XRP - XRP" },
  { value: "DOGE", label: "DOGE - Dogecoin" },
  { value: "USDC", label: "USDC - USD Coin" },
  { value: "USDT", label: "USDT - Tether" },
] as const;

export const LANGUAGE_OPTIONS = [
  { value: "en", label: "English" },
  { value: "de", label: "Deutsch" },
  { value: "fr", label: "Francais" },
  { value: "it", label: "Italiano" },
  { value: "pt", label: "Portugues" },
  { value: "es", label: "Espanol" },
] as const;

export const REGION_OPTIONS = [
  { value: "CH", label: "Switzerland" },
  { value: "US", label: "United States" },
  { value: "GB", label: "United Kingdom" },
  { value: "DE", label: "Germany" },
  { value: "FR", label: "France" },
  { value: "IT", label: "Italy" },
  { value: "PT", label: "Portugal" },
  { value: "BR", label: "Brazil" },
  { value: "ES", label: "Spain" },
] as const;

export function currencyOptionsWith(value: string | null | undefined) {
  const normalized = value?.trim().toUpperCase();
  if (
    !normalized ||
    CURRENCY_OPTIONS.some((option) => option.value === normalized)
  ) {
    return CURRENCY_OPTIONS;
  }
  return [
    { value: normalized, label: `${normalized} - Imported currency` },
    ...CURRENCY_OPTIONS,
  ];
}
