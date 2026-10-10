import type { ReactNode } from "react";

export const metadata = {
  title: "Sutra Mini",
  description: "Jobwork receipts and accounting voucher sync",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body
        style={{
          margin: 0,
          fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, sans-serif",
          background: "#f6f7f4",
          color: "#182621",
        }}
      >
        {children}
      </body>
    </html>
  );
}
