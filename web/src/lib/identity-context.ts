import { createContext, useContext } from "react";
import type { Identity } from "./contracts";

export const IdentityContext = createContext<Identity | null>(null);
export function useIdentity() {
  const value = useContext(IdentityContext);
  if (!value)
    throw new Error("useIdentity must be used in an authenticated route");
  return value;
}
