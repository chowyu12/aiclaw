export interface ConfigurationField {
  key: string;
  label: string;
  kind: "text" | "password" | "json" | "boolean" | "select";
  value: string | boolean;
  required?: boolean;
  isSet?: boolean;
  secret?: boolean;
  choices?: string[];
}
export interface ConfigurationCard {
  id: string;
  sessionId: string;
  category: string;
  operation: string;
  title: string;
  note: string;
  fields: ConfigurationField[];
}
export interface ConfigurationReply {
  id: string;
  cancel?: boolean;
  values?: Record<string, string | boolean>;
}
export interface ConfigurationOutcome { text: string; }
