import { db } from "@/db";
import { categories } from "@/db/schema";

const CATEGORIES = [
  // PostFinance categories
  { name: "Composition of assets // Retirement planning", parentGroup: "Composition of assets", subGroup: "Retirement planning" },
  { name: "Expenditures other", parentGroup: "Expenditures other", subGroup: null },
  { name: "Finances // Cash withdrawals", parentGroup: "Finances", subGroup: "Cash withdrawals" },
  { name: "Finances // Donations", parentGroup: "Finances", subGroup: "Donations" },
  { name: "Finances // Fees", parentGroup: "Finances", subGroup: "Fees" },
  { name: "Finances // Insurance", parentGroup: "Finances", subGroup: "Insurance" },
  { name: "Finances // Taxes", parentGroup: "Finances", subGroup: "Taxes" },
  { name: "Finances // Transfers other", parentGroup: "Finances", subGroup: "Transfers other" },
  { name: "Income", parentGroup: "Income", subGroup: null },
  { name: "Leisure // Culture", parentGroup: "Leisure", subGroup: "Culture" },
  { name: "Leisure // Flights", parentGroup: "Leisure", subGroup: "Flights" },
  { name: "Leisure // Food services", parentGroup: "Leisure", subGroup: "Food services" },
  { name: "Leisure // Sports", parentGroup: "Leisure", subGroup: "Sports" },
  { name: "Leisure // Travel & Experience", parentGroup: "Leisure", subGroup: "Travel & Experience" },
  { name: "Living // Healthcare", parentGroup: "Living", subGroup: "Healthcare" },
  { name: "Living // Living other", parentGroup: "Living", subGroup: "Living other" },
  { name: "Mobility // Private transport", parentGroup: "Mobility", subGroup: "Private transport" },
  { name: "Mobility // Public transport", parentGroup: "Mobility", subGroup: "Public transport" },
  { name: "Other incomings", parentGroup: "Other incomings", subGroup: null },
  { name: "Refunds", parentGroup: "Refunds", subGroup: null },
  { name: "Residing // Communication", parentGroup: "Residing", subGroup: "Communication" },
  { name: "Residing // Furniture", parentGroup: "Residing", subGroup: "Furniture" },
  { name: "Residing // Household", parentGroup: "Residing", subGroup: "Household" },
  { name: "Residing // Residing other", parentGroup: "Residing", subGroup: "Residing other" },
  { name: "Residing // additional costs", parentGroup: "Residing", subGroup: "additional costs" },
  { name: "Shopping // Fashion", parentGroup: "Shopping", subGroup: "Fashion" },
  { name: "Shopping // Grocery", parentGroup: "Shopping", subGroup: "Grocery" },
  { name: "Shopping // Literature", parentGroup: "Shopping", subGroup: "Literature" },
  { name: "Shopping // Online shopping", parentGroup: "Shopping", subGroup: "Online shopping" },
  { name: "Shopping // Shopping other", parentGroup: "Shopping", subGroup: "Shopping other" },
  { name: "Shopping // Supermarkets", parentGroup: "Shopping", subGroup: "Supermarkets" },
  { name: "Transfers", parentGroup: "Transfers", subGroup: null },
  // Revolut-specific categories
  { name: "Finances // FX Conversion", parentGroup: "Finances", subGroup: "FX Conversion" },
  { name: "Income // Interest", parentGroup: "Income", subGroup: "Interest" },
  { name: "Income // Interest Reinvested", parentGroup: "Income", subGroup: "Interest Reinvested" },
  { name: "Income // Salary", parentGroup: "Income", subGroup: "Salary" },
  { name: "Leisure // Subscriptions", parentGroup: "Leisure", subGroup: "Subscriptions" },
  { name: "Finances // Investments", parentGroup: "Finances", subGroup: "Investments" },
  { name: "Finances // Savings", parentGroup: "Finances", subGroup: "Savings" },
  { name: "Leisure // Entertainment", parentGroup: "Leisure", subGroup: "Entertainment" },
  { name: "Living // Rent", parentGroup: "Living", subGroup: "Rent" },
  { name: "Living // Utilities", parentGroup: "Living", subGroup: "Utilities" },
];

async function main() {
  console.log(`Seeding ${CATEGORIES.length} categories...`);

  for (const cat of CATEGORIES) {
    await db
      .insert(categories)
      .values(cat)
      .onConflictDoNothing({ target: categories.name });
  }

  console.log("Done.");
  process.exit(0);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
