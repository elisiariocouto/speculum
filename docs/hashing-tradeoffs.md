# Hashing Tradeoffs in Terraform Provider Mirrors

## The Problem

When implementing the [Terraform Provider Network Mirror Protocol](https://developer.hashicorp.com/terraform/internals/provider-network-mirror-protocol), there's a fundamental mismatch between what information is easily available when
requesting information from a registry implementing [Terraform Provider Protocol](https://developer.hashicorp.com/terraform/internals/provider-registry-protocol) and what the Terraform CLI wants.

## The Protocol Flow

### 1. Index Request (`index.json`)

Client requests: `GET /registry.terraform.io/hashicorp/random/index.json`

To serve this, Speculum:
- Performs service discovery: `GET https://registry.terraform.io/.well-known/terraform.json`
- Gets the providers API endpoint (e.g., `https://registry.terraform.io/v1/providers/`)
- Calls: `GET https://registry.terraform.io/v1/providers/hashicorp/random/versions`

This returns a list of versions with their supported platforms:
```json
{
  "versions": [
    {
      "version": "2.0.0",
      "protocols": ["4.0", "5.1"],
      "platforms": [
        {"os": "darwin", "arch": "amd64"},
        {"os": "linux", "arch": "amd64"},
        {"os": "linux", "arch": "arm"},
        {"os": "windows", "arch": "amd64"}
      ]
    }
  ]
}
```

Speculum returns the following document, as per spec.
```json
{
  "versions": {
    "2.0.0": {},
    "2.0.1": {}
  }
}
```

**Cost: 1-2 API calls** (service discovery + versions list)

### 2. Version Metadata Request (`2.0.0.json`)

Client requests: `GET /registry.terraform.io/hashicorp/random/2.0.0.json`

Speculum needs to return:
```json
{
  "archives": {
    "darwin_amd64": {
      "url": "terraform-provider-random_2.0.0_darwin_amd64.zip",
      "hashes": ["h1:4A07+ZFc2wgJwo8YNlQpr1rVlgUDlxXHhPJciaPY5gs="]
    },
    "linux_amd64": {
      "url": "terraform-provider-random_2.0.0_linux_amd64.zip",
      "hashes": ["h1:lCJCxf/LIowc2IGS9TPjWDyXY4nOmdGdfcwwDQCOURQ="]
    }
  }
}
```

The `hashes` key is optional as [per spec](https://developer.hashicorp.com/terraform/internals/provider-network-mirror-protocol#hashes).

**The problem:** The `/versions` endpoint we already called gives us the platforms list, but **NOT the hashes**.

### 3. Getting Hashes: Two Expensive Options

#### Option A: Multiple API Calls (One Per Platform)

To get hashes without downloading, we'd need to call the downloads endpoint for each platform:

```
GET /v1/providers/hashicorp/random/2.0.0/downloads/darwin/amd64
GET /v1/providers/hashicorp/random/2.0.0/downloads/linux/amd64
GET /v1/providers/hashicorp/random/2.0.0/downloads/linux/arm
GET /v1/providers/hashicorp/random/2.0.0/downloads/windows/amd64
```

Each returns metadata including hashes:
```json
{
  "protocols": ["5.0"],
  "os": "linux",
  "arch": "amd64",
  "filename": "terraform-provider-random_2.0.0_linux_amd64.zip",
  "download_url": "https://releases.hashicorp.com/terraform-provider-random/2.0.0/terraform-provider-random_2.0.0_linux_amd64.zip",
  "shasums_url": "https://releases.hashicorp.com/terraform-provider-random/2.0.0/terraform-provider-random_2.0.0_SHA256SUMS",
  "shasums_signature_url": "https://releases.hashicorp.com/terraform-provider-random/2.0.0/terraform-provider-random_2.0.0_SHA256SUMS.sig",
  "shasum": "5f9c7aa76b7c34d722fc9123208e26b22d60440cb47150dd04733b9b94f4541a",
  "signing_keys": { /* ... */ }
}
```

Here's a pitfall - the `shasum` is a SHA256 hash of the zip, that we can use to return on the previous `hashes` key using the prefix `zh:`.

This might do the trick but there's another layer of problems, Terraform is moving to `h1:` hashes which are SHA256 of the zip **contents** so we need to actually download every package, unzip it to a temporary location
and then calculate the `h1` hash.

**Cost for this provider with 4 platforms: 4 API calls (8 if we want to support h1 hashes)**

#### Option B: Eager Archive Downloads

Download every archive immediately and compute the h1 hash ourselves:

1. Download all platform archives for the requested version
2. Extract each zip to a temporary directory
3. Run `golang.org/x/mod/sumdb/dirhash.HashDir()` to compute h1 hash
4. Store archive and hash in cache
5. Return metadata with hashes

**Cost: Download and process ALL archives for the version** (e.g., 6 × 50MB downloads for 6 platforms)

### 4. Archive Download (The Lazy Approach)

When the client actually requests an archive:
```
GET /terraform-provider-random_2.0.0_linux_amd64.zip
```

Only then do we:
1. Call the download endpoint for that specific platform: `GET /v1/providers/hashicorp/random/2.0.0/download/linux/amd64`
2. Download the archive from the returned `download_url`
3. Cache it and serve it

**Cost: 1 API call + 1 download per archive actually used**

## The Solution: Hashes Are Optional

According to the [Terraform Provider Network Mirror Protocol specification](https://developer.hashicorp.com/terraform/internals/provider-network-mirror-protocol), **the `hashes` field is optional**.

By omitting hashes from the version metadata response, Speculum can:

1. ✅ Serve index.json with just 1-2 API calls
2. ✅ Serve version metadata instantly without any additional API calls (reuse platform list from step 1)
3. ✅ Download and cache archives lazily (only when actually requested)
4. ✅ Scale to large providers with hundreds of versions and multiple platforms
5. ✅ Minimize upstream registry load
6. ✅ Minimize bandwidth usage
7. ✅ Provide faster response times

## Tradeoffs

### Without Hashes (Current Approach)

**Pros:**
- Fast responses (no blocking on downloads or extra API calls)
- Minimal upstream load
- Bandwidth efficient (only download what's used)
- Simple caching strategy
- Works well with any provider size

**Cons:**
- Terraform CLI won't have hashes to verify downloads against
- Less security verification at download time

### With Hashes (Eager or Multiple Calls)

**Pros:**
- Terraform CLI can verify hashes
- Matches behavior of `terraform providers mirror` command

**Cons:**
- Significantly slower response times
- Heavy upstream load (especially for large providers)
- Bandwidth intensive (downloading archives never used)
- Complex caching logic
- Poor scalability

## Conclusion

For a caching proxy mirror like Speculum, the lazy approach without hashes is the right tradeoff:

- Hashes are optional per the protocol specification
- The primary goal is to cache and accelerate provider downloads
- Terraform already uses signature verification for security
- The performance and scalability benefits far outweigh the missing hashes
- Hashes can still be computed and cached on-demand when archives are first downloaded (for future requests)

The architecture can always be extended later to support optional eager hashing for users who require it, without changing the core lazy-download model.
