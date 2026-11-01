import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = {
  title: "Relay · LLM Gateway",
  description: "Manage models, access and complete request history.",
};
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
