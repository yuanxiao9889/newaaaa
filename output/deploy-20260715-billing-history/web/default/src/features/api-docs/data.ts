/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export type ModelProtocol = "OpenAI Images" | "Gemini generateContent";

export type ParameterDoc = {
  name: string;
  type: string;
  required: string;
  values: string;
  description: string;
};

export type EndpointDoc = {
  method: "POST";
  path: string;
  contentType: string;
  purpose: string;
};

export type ImageModelDoc = {
  id: string;
  model: string;
  protocol: ModelProtocol;
  family: "openai" | "gemini";
  summary: string;
  recommendedFor: string;
  endpoints: EndpointDoc[];
  ratios: string[];
  resolutions: string[];
  parameters: ParameterDoc[];
  supportsEditing: boolean;
  responsePath: string;
  requestNotes: string[];
};

export const gptImageRatios = [
  "1:1",
  "2:3",
  "3:2",
  "3:4",
  "4:3",
  "4:5",
  "5:4",
  "9:16",
  "16:9",
  "21:9",
  "9:21",
  "1:3",
  "3:1",
  "1:2",
  "2:1",
];

export const genericImageRatios = [
  "1:1",
  "1:4",
  "1:8",
  "2:3",
  "3:2",
  "3:4",
  "4:1",
  "4:3",
  "4:5",
  "5:4",
  "8:1",
  "9:16",
  "16:9",
  "21:9",
  "9:21",
];

const openAIEndpoints: EndpointDoc[] = [
  {
    method: "POST",
    path: "/v1/images/generations",
    contentType: "application/json",
    purpose: "Generate an image from a text prompt.",
  },
  {
    method: "POST",
    path: "/v1/images/edits",
    contentType: "multipart/form-data",
    purpose: "Edit or restyle one or more reference images.",
  },
];

const grokEndpoints: EndpointDoc[] = [
  {
    method: "POST",
    path: "/v1/images/generations",
    contentType: "application/json",
    purpose:
      "Generate an image or submit embedded reference images in one JSON request.",
  },
];

const commonOpenAIParameters: ParameterDoc[] = [
  {
    name: "model",
    type: "string",
    required: "Required",
    values: "Exact model ID",
    description: "The model ID must exactly match the selected model.",
  },
  {
    name: "prompt",
    type: "string",
    required: "Required",
    values: "UTF-8 text",
    description:
      "Describe the subject, composition, style, lighting, and constraints.",
  },
  {
    name: "n",
    type: "integer",
    required: "Required by client",
    values: "1",
    description: "Storyboard Copilot sends exactly one image per task.",
  },
  {
    name: "size",
    type: "string",
    required: "Required by client",
    values: "2048x1152 (width-by-height)",
    description:
      "Send a lowercase pixel string such as 2048x1152; the first number is width and the second is height.",
  },
  {
    name: "quality",
    type: "string",
    required: "Required by client",
    values: "low | medium | high",
    description:
      "Storyboard Copilot sends medium by default; its OOpii model controls expose low, medium, and high.",
  },
  {
    name: "response_format",
    type: "string",
    required: "Not sent by client",
    values: "Omitted",
    description:
      "Storyboard Copilot omits this field for both generations and multipart edits; read whichever url or b64_json field the upstream response provides.",
  },
  {
    name: "image / image[]",
    type: "file",
    required: "Edits only",
    values: "PNG upload",
    description:
      "Storyboard Copilot converts references to PNG. One reference uses image; multiple references use repeated image[].",
  },
];

const allImageParameters: ParameterDoc[] = [
  ...commonOpenAIParameters.slice(0, 4),
  {
    name: "aspect_ratio",
    type: "string",
    required: "Required by client",
    values: "Selected supported ratio",
    description:
      "Sent together with the exact pixel size for all-image-2 and all-image-2-DD.",
  },
  ...commonOpenAIParameters.slice(4),
];

const gptImage2Parameters: ParameterDoc[] = commonOpenAIParameters;

const grokParameters: ParameterDoc[] = [
  {
    name: "model",
    type: "string",
    required: "Required",
    values: "grok-imagine-image-pro",
    description: "Use the exact OOpii transport model ID.",
  },
  {
    name: "prompt",
    type: "string",
    required: "Required",
    values: "UTF-8 text",
    description:
      "Storyboard Copilot adds ratio and reference-resolution hints when needed.",
  },
  {
    name: "n",
    type: "integer",
    required: "Required by client",
    values: "1",
    description: "Storyboard Copilot sends exactly one image per task.",
  },
  {
    name: "response_format",
    type: "string",
    required: "Required by client",
    values: "b64_json",
    description:
      "Storyboard Copilot requests a base64 image response for Grok.",
  },
  {
    name: "size",
    type: "string",
    required: "Ratio-dependent",
    values: "Pixel size or selected aspect ratio",
    description:
      "Text-to-image uses mapped pixel sizes. With reference images, Storyboard Copilot overrides size with the selected aspect-ratio enum; 3:2 text-to-image still omits size.",
  },
  {
    name: "aspect_ratio",
    type: "string",
    required: "Required by client",
    values: "1:1 | 16:9 | 9:16 | 2:3 | 3:2",
    description: "Primary Grok canvas-ratio field.",
  },
  {
    name: "reference_images",
    type: "string[]",
    required: "Reference images only",
    values: "data:image/...;base64,...",
    description:
      "Embedded data URLs. Grok reference requests remain JSON generations requests and do not use multipart edits.",
  },
  {
    name: "output_resolution / resolution / image_size",
    type: "string",
    required: "Reference images only",
    values: "1K | 2K | 4K",
    description:
      "Compatibility aliases sent together for Grok reference-image requests.",
  },
  {
    name: "aspectRatio",
    type: "string",
    required: "Compatibility only",
    values: "Selected supported ratio",
    description:
      "Sent at the top level for every Grok reference-image request and for the 3:2 text-to-image compatibility path.",
  },
  {
    name: "generationConfig.imageConfig",
    type: "object",
    required: "Reference images only",
    values: "{ imageSize, aspectRatio, size }",
    description:
      "For reference-image requests, imageSize carries 1K, 2K, or 4K while aspectRatio and size both carry the selected ratio.",
  },
  {
    name: "extra_body",
    type: "object",
    required: "3:2 only",
    values: "{ aspect_ratio, aspectRatio }",
    description:
      "Additional ratio aliases sent only for the Grok 3:2 compatibility path.",
  },
];

const geminiEndpoints: EndpointDoc[] = [
  {
    method: "POST",
    path: "/v1beta/models/{model}:generateContent",
    contentType: "application/json",
    purpose: "Generate or edit an image with Gemini-compatible content parts.",
  },
];

const geminiParameters: ParameterDoc[] = [
  {
    name: "contents",
    type: "array",
    required: "Required",
    values: "Content[]",
    description:
      "Conversation content. Storyboard Copilot sends one user content item.",
  },
  {
    name: "contents[].parts[].text",
    type: "string",
    required: "Required",
    values: "Prompt plus preference suffix",
    description:
      "The first request appends [size: 2K, aspect ratio: 16:9] using the selected values.",
  },
  {
    name: "contents[].parts[].inlineData",
    type: "object",
    required: "Reference images only",
    values: '{ mimeType: "image/png", data }',
    description:
      "Storyboard Copilot converts each reference to PNG and adds one inlineData part per image.",
  },
  {
    name: "generationConfig.topP",
    type: "number",
    required: "Required by client",
    values: "0.95",
    description: "Storyboard Copilot sends 0.95 on its Gemini image attempts.",
  },
  {
    name: "generationConfig.responseModalities",
    type: "string[]",
    required: "Required by client",
    values: '["IMAGE"]',
    description: "Requests image output from the Gemini-compatible endpoint.",
  },
  {
    name: "generationConfig.imageConfig.aspectRatio",
    type: "string",
    required: "First attempt",
    values: "Selected supported ratio",
    description:
      "Sent on the first request and removed automatically on the no-aspect compatibility retry.",
  },
  {
    name: "generationConfig.imageConfig.imageSize",
    type: "string",
    required: "Required by client",
    values: "1K | 2K | 4K",
    description: "Sets the requested image resolution tier.",
  },
];

export const imageModels: ImageModelDoc[] = [
  {
    id: "all-image-2",
    model: "all-image-2",
    protocol: "OpenAI Images",
    family: "openai",
    summary:
      "Recommended OOpii aggregate image model with generation and multi-reference editing.",
    recommendedFor:
      "Storyboard frames, character consistency, and general production image tasks.",
    endpoints: openAIEndpoints,
    ratios: gptImageRatios,
    resolutions: ["1K", "2K", "4K"],
    parameters: allImageParameters,
    supportsEditing: true,
    responsePath: "data[0].url or data[0].b64_json",
    requestNotes: [
      "Storyboard Copilot sends n=1, exact size, aspect_ratio, and quality for both generations and multipart edits without response_format.",
      "For edits, one normalized PNG reference uses image; multiple references use repeated image[].",
    ],
  },
  {
    id: "all-image-2-dd",
    model: "all-image-2-DD",
    protocol: "OpenAI Images",
    family: "openai",
    summary:
      "DD routing variant of all-image-2 with the same OpenAI Images request contract.",
    recommendedFor:
      "Projects that explicitly need the OOpii DD route while keeping the same client code.",
    endpoints: openAIEndpoints,
    ratios: gptImageRatios,
    resolutions: ["1K", "2K", "4K"],
    parameters: allImageParameters,
    supportsEditing: true,
    responsePath: "data[0].url or data[0].b64_json",
    requestNotes: [
      "The transport model ID remains all-image-2-DD, including the uppercase DD suffix.",
      "Storyboard Copilot sends n=1, exact size, aspect_ratio, and quality for both generations and multipart edits without response_format.",
    ],
  },
  {
    id: "gpt-image-2",
    model: "gpt-image-2",
    protocol: "OpenAI Images",
    family: "openai",
    summary:
      "OOpii official-mode image route using exact pixel sizes and OpenAI Images semantics.",
    recommendedFor:
      "High-fidelity image generation where exact output pixels and quality must be explicit.",
    endpoints: openAIEndpoints,
    ratios: gptImageRatios,
    resolutions: ["1K", "2K", "4K"],
    parameters: gptImage2Parameters,
    supportsEditing: true,
    responsePath: "data[0].url or data[0].b64_json",
    requestNotes: [
      "Storyboard Copilot derives the ratio from size and never sends aspect_ratio, image_backend, or response_format for either generations or multipart edits.",
      "For edits, one normalized PNG reference uses image; multiple references use repeated image[].",
    ],
  },
  {
    id: "grok-imagine-image-pro",
    model: "grok-imagine-image-pro",
    protocol: "OpenAI Images",
    family: "openai",
    summary:
      "Grok-compatible image generation and embedded-reference editing through OOpii.",
    recommendedFor:
      "Fast creative iteration with common landscape, portrait, and square ratios.",
    endpoints: grokEndpoints,
    ratios: ["1:1", "16:9", "9:16", "2:3", "3:2"],
    resolutions: ["Auto", "1K", "2K", "4K"],
    parameters: grokParameters,
    supportsEditing: true,
    responsePath: "data[0].url or data[0].b64_json",
    requestNotes: [
      "Text-only and reference-image operations both use /v1/images/generations with application/json.",
      "Storyboard Copilot always sends response_format=b64_json and never sends quality for this model.",
      "Reference-image requests send size and aspectRatio as the selected ratio, resolution aliases as 1K, 2K, or 4K, and a matching generationConfig.imageConfig object.",
      "Asynchronous mode appends async=true to Grok text-to-image and reference-image generation requests.",
      "For 3:2 text-to-image, size is omitted and aspect_ratio, aspectRatio, and extra_body ratio aliases are sent for compatibility.",
    ],
  },
  {
    id: "monkey-image-pro",
    model: "monkey-image-pro",
    protocol: "Gemini generateContent",
    family: "gemini",
    summary:
      "Gemini-compatible high-quality image route with inlineData reference-image support.",
    recommendedFor:
      "Detailed image generation and edits that need broad aspect-ratio coverage.",
    endpoints: geminiEndpoints,
    ratios: genericImageRatios,
    resolutions: ["1K", "2K", "4K"],
    parameters: geminiParameters,
    supportsEditing: true,
    responsePath: "candidates[].content.parts[].inlineData.data",
    requestNotes: [
      "The first request sends topP=0.95, responseModalities=[IMAGE], imageSize, aspectRatio, and a matching prompt suffix.",
      "If the first request is rejected, Storyboard Copilot retries without aspectRatio and the prompt suffix; reference images may also be resized to a 1536 or 1024 pixel longest edge.",
    ],
  },
  {
    id: "monkey-image-flash-2",
    model: "monkey-image-flash 2",
    protocol: "Gemini generateContent",
    family: "gemini",
    summary:
      "Faster Gemini-compatible OOpii image route with the same content-parts request shape.",
    recommendedFor:
      "Rapid previews, batch storyboard drafts, and lower-latency iterations.",
    endpoints: geminiEndpoints,
    ratios: genericImageRatios,
    resolutions: ["1K", "2K", "4K"],
    parameters: geminiParameters,
    supportsEditing: true,
    responsePath: "candidates[].content.parts[].inlineData.data",
    requestNotes: [
      "The model ID contains a space, so the request path encodes it as monkey-image-flash%202.",
      "The first request and compatibility retries use the same topP, prompt-suffix, and reference-image rules as monkey-image-pro.",
    ],
  },
];

export const sizeRows = [
  ["1K", "1:1", "1024x1024"],
  ["1K", "16:9", "1280x720"],
  ["1K", "9:16", "720x1280"],
  ["1K", "3:2", "1248x832"],
  ["1K", "2:3", "832x1248"],
  ["2K", "1:1", "2048x2048"],
  ["2K", "16:9", "2048x1152"],
  ["2K", "9:16", "1152x2048"],
  ["2K", "3:2", "2496x1664"],
  ["2K", "2:3", "1664x2496"],
  ["4K", "1:1", "2880x2880"],
  ["4K", "16:9", "3840x2160"],
  ["4K", "9:16", "2160x3840"],
  ["4K", "3:2", "3504x2336"],
  ["4K", "2:3", "2336x3504"],
] as const;
