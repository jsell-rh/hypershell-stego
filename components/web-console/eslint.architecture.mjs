const sdkPattern = {
  group: ["**/out/tssdk/**"],
  message:
    "Import the generated SDK only from an API or session adapter or composition root.",
};

const telemetryPattern = {
  group: ["@opentelemetry/*"],
  message:
    "Import telemetry vendors only from an observability adapter or composition root.",
};

const externalEffectSyntax = [
  "error",
  {
    selector:
      "CallExpression[callee.object.name='Date'][callee.property.name='now']",
    message: "Read time through an application-owned clock port.",
  },
  {
    selector:
      "CallExpression[callee.object.name='Math'][callee.property.name='random']",
    message: "Read randomness through an application-owned port.",
  },
  {
    selector: "NewExpression[callee.name='Date']",
    message: "Read time through an application-owned clock port.",
  },
];

export const rawDiagnosticRules = {
  "no-console": "error",
  "no-restricted-properties": [
    "error",
    {
      object: "process",
      property: "stderr",
      message: "Use a named structured observability adapter.",
    },
    {
      object: "process",
      property: "stdout",
      message: "Use a named structured observability adapter.",
    },
  ],
};

export const sdkImportRule = ["error", { patterns: [sdkPattern] }];

export const browserSdkAndTelemetryImportRule = [
  "error",
  {
    paths: [
      {
        name: "web-vitals",
        message:
          "Use web-vitals only from an observability adapter or composition root.",
      },
    ],
    patterns: [sdkPattern, telemetryPattern],
  },
];

export const browserTelemetryImportRule = [
  "error",
  {
    paths: [
      {
        name: "web-vitals",
        message: "Keep telemetry out of API adapters.",
      },
    ],
    patterns: [
      {
        group: ["@opentelemetry/*"],
        message: "Keep telemetry out of API adapters.",
      },
    ],
  },
];

export const browserApplicationRules = {
  "no-restricted-globals": [
    "error",
    ...["document", "navigator", "window"].map((name) => ({
      name,
      message: "Keep browser APIs outside application and domain code.",
    })),
    ...["localStorage", "sessionStorage"].map((name) => ({
      name,
      message: "Access storage through an application-owned port.",
    })),
    {
      name: "fetch",
      message: "Access APIs through an application-owned port.",
    },
  ],
  "no-restricted-imports": [
    "error",
    {
      paths: [
        {
          name: "@openshift-online/hypershell-domain-probes/fan-out",
          message:
            "Keep the concrete probe fan-out in observability adapters or composition.",
        },
        ...[
          "@patternfly/react-core",
          "@tanstack/react-query",
          "react",
          "react-dom",
          "react-router",
        ].map((name) => ({
          name,
          message: "Keep UI frameworks in presentation adapters.",
        })),
        {
          name: "web-vitals",
          message: "Publish domain facts through the probe port.",
        },
      ],
      patterns: [
        sdkPattern,
        {
          group: [
            "@opentelemetry/*",
            "@patternfly/*",
            "@tanstack/*",
            "**/adapters/**",
            "**/composition/**",
          ],
          message: "Application and domain dependencies must point inward.",
        },
      ],
    },
  ],
  "no-restricted-syntax": externalEffectSyntax,
};
