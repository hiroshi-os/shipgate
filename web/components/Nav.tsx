"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { getActor, OPERATORS, setActor } from "@/lib/api";

export function Nav() {
  const path = usePathname();
  const [actor, set] = useState("alice");
  useEffect(() => {
    set(getActor());
    const on = () => set(getActor());
    window.addEventListener("shipgate-actor", on);
    return () => window.removeEventListener("shipgate-actor", on);
  }, []);
  return (
    <header className="nav">
      <Link href="/" className="brand">
        <svg className="brand-mark" viewBox="0 0 32 32" aria-hidden>
          <rect x="3" y="4" width="6" height="24" rx="1" fill="#e2ff5a" />
          <rect x="23" y="4" width="6" height="24" rx="1" fill="#e2ff5a" />
          <rect x="9" y="14" width="14" height="4" fill="#f4f0e6" />
        </svg>
        <span>
          Shipgate
          <small>promotion airlock</small>
        </span>
      </Link>
      <nav className="nav-links">
        <Link className={path === "/" ? "on" : ""} href="/">
          Board
        </Link>
        <Link className={path === "/audit" ? "on" : ""} href="/audit">
          Flight recorder
        </Link>
      </nav>
      <div className="actor">
        operator
        <select
          value={actor}
          onChange={(e) => {
            setActor(e.target.value);
            set(e.target.value);
          }}
        >
          {OPERATORS.map((o) => (
            <option key={o}>{o}</option>
          ))}
        </select>
      </div>
    </header>
  );
}
