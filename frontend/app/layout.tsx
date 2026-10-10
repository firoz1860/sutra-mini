import type { ReactNode } from "react";
import { Hanken_Grotesk, Fraunces } from "next/font/google";
import "./globals.css";

const sans = Hanken_Grotesk({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
  variable: "--font-sans",
  display: "swap",
});

const display = Fraunces({
  subsets: ["latin"],
  weight: ["500", "600"],
  style: ["normal"],
  variable: "--font-display",
  display: "swap",
});

export const metadata = {
  title: "Sutra Mini",
  description: "Jobwork receipts and accounting voucher sync",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" className={`${sans.variable} ${display.variable}`}>
      <body>{children}</body>
    </html>
  );
}
