# Telegram Bot CA Scraper

## Overview
This project aims to create a system that identifies, verifies, and ranks safe and promising crypto projects (contract addresses, or CAs). The curated results will be shared in a token-gated Telegram group accessible to users holding a specific token (e.g., one "Demon"). This initiative empowers smaller investors to make informed decisions, grow their portfolios, and potentially upgrade their token tier.

## How It Works
1. **Scraping Contract Addresses**: The system collects contract addresses from popular Telegram volume bots in real-time.
2. **Verification**: Each contract address is checked for safety and legitimacy using a verification system.
3. **Ranking**: Verified projects are analyzed and ranked based on safety and aesthetic/branding quality.
4. **Token-Gated Access**: Only users who hold at least one "Demon" token can access the curated list in a private Telegram group.

## Objectives
- Help smaller investors discover safe and promising projects.
- Build a strong community around the Demon token by providing exclusive value.
- Encourage portfolio growth, leading to increased demand for Demon tokens.

## What’s Needed
### 1. Technology
- **Bot** to scrape contract addresses from Telegram volume bots.
- **Verification system** to ensure the safety and legitimacy of each address.
- **Token-gated Telegram group** that allows verified wallets with at least one Demon token to join.

### 2. Resources
- **Development Team**: To build and maintain the bot, verification system, and Telegram integration.
- **Hosting & Servers**: To run the bots and store necessary data securely.
- **APIs/Tools**: Verification tools to analyze contract addresses for safety.

### 3. Time
- **Estimated project duration**: 2-3 weeks
  - Phase 1: Planning & Setup (2 days)
  - Phase 2: Development & Integration (2 weeks)
  - Phase 3: Testing & Launch (1 week)

### 4. Cost
- **Development**: $8,000 - $12,000
- **Tools & APIs**: $100 - $500/month
- **Hosting**: $50 - $150/month

## Value to the Community
- **Lower Barrier to Entry**: Allows smaller investors to access safe projects without extensive research.
- **Exclusive Perks**: Token-gated access ensures only committed members benefit, increasing demand for Demon tokens.
- **Scalable Model**: As the community grows, the system can expand to include additional features or data sources.

## Success Metrics
- **Community Growth**: Increase in Demon token holders.
- **Engagement**: Number of active members in the gated Telegram group.
- **User Satisfaction**: Positive feedback on project quality and safety.

## Call to Action
This project will:
- Provide real value to Demon holders.
- Encourage community growth.
- Position the Demon token as a trusted resource for crypto investors.

## Workflow
### 1. Define Filter Parameters
To ensure meaningful results, the bot must use specific filters, including:
- **Volume Metrics**:
  - Volume per 5 minutes: Detect initial spikes in trading activity.
  - Volume per 15 minutes: Identify sustained activity in a short period.
  - Volume per 30 minutes: Analyze medium-term trends for better reliability.
  - Volume per 1 hour: Highlight projects with consistent activity.
  - Volume per 3 hours: Evaluate stability over a significant trading window.
  - Volume per 6 hours: Recognize projects with long-term trading interest.
- **Buy vs. Sell Metrics**:
  - Buys vs. Sells per 30 seconds: Track real-time sentiment and trading behavior.

### 2. Scrape Data from Volume Bots
The bot integrates with existing Telegram volume bots providing trading data for Solana projects. Below is a list of prominent volume bots:
- **Solana Big Volume Bot**
- **SOL Sniper**
- **SolAlert**
- **DeFiVolumeBot (for Solana)**
- **SOL Radar**
- **Solana Alpha Bot**
- **ChartUp**
- **Volume X**
- **TSUNAMI Bot**
- **Orbitt MM**
- **Volana**
- **Trojan on Solana**
- **SolanaBots Studio**

### 3. Process Workflow
#### Step 1: Data Collection
- Continuously scrape trading data from the listed Telegram bots.
- Store raw data in a secure database (e.g., PostgreSQL, Railway.app).

#### Step 2: Apply Filters
- Analyze collected data using predefined parameters:
  - Volume thresholds (5m, 15m, 30m, etc.).
  - Buy vs. sell activity.
- Identify patterns that indicate promising or risky projects.

#### Step 3: Verification
- Use tools like Mugetsu to verify contract safety and legitimacy.
- Exclude contracts flagged for malicious behavior.

#### Step 4: Ranking
- Rank verified contracts based on safety, volume, and branding quality.

#### Step 5: Dissemination
- Share the curated list in the token-gated Telegram group for Demon holders.

## Architecture
### 1. Core Tech Stack
- **Programming Language**: Go
  - High performance and low latency.
  - Efficient concurrency with Goroutines.
  - Built-in networking libraries for HTTP requests and WebSocket handling.
- **Database**:
  - Redis: For in-memory caching and ultra-fast read/write operations.
  - PostgreSQL/MySQL: For persistent storage of processed data.
- **Message Queue**:
  - Kafka or RabbitMQ: For managing a large volume of incoming messages and ensuring reliable delivery to processing workers.

### 2. System Design
#### A. Telegram Bot Integration
- **Webhook-Based Approach**:
  - Configure your bot to use Telegram’s webhook method.
  - Telegram pushes new messages directly to your server, eliminating polling delays.
  - Example: Bot receives an incoming message via a webhook and immediately pushes it to a processing queue.
- **Rate Limiting**:
  - Telegram API rate limits are per bot, so batch or parallelize responses if needed.

#### B. Message Processing
- **Message Queueing**:
  - Use Kafka or RabbitMQ to enqueue messages received by the bot.
  - Each message includes:
    - Sender ID
    - Timestamp
    - Message text
    - Metadata (e.g., bot name, volume metrics).
- **Goroutines for Concurrency**:
  - Spawn lightweight Goroutines to handle messages in parallel.
  - Goroutines process messages, parse data, and filter for relevance (e.g., volume metrics, buy/sell data).

#### C. Real-Time Data Processing
- **Stream Processing Framework**:
  - Integrate with tools like Apache Flink or Spark Streaming for real-time analytics if advanced processing is needed.
  - Alternatively, keep the logic lightweight within Go for simpler setups.
- **In-Memory Caching**:
  - Store intermediate results (e.g., aggregated metrics) in Redis to avoid recomputation.
  - Example: Aggregate "volume per 5m" data in Redis and update it every second.

#### D. Scalable Deployment
- **Load Balancing**:
  - Use a load balancer (e.g., Nginx, AWS ALB) to distribute webhook traffic across multiple bot instances.
- **Horizontal Scaling**:
  - Deploy multiple bot instances in containers (Docker + Kubernetes) to scale horizontally.
  - Autoscale instances based on CPU/memory usage or message volume.
- **Monitoring**:
  - Use Prometheus and Grafana for monitoring system performance, message throughput, and API latency.

### Workflow
1. **Message Reception**:
   - Telegram sends a message to your bot via a webhook.
2. **Queueing**:
   - Bot pushes the message into Kafka/RabbitMQ for processing.
3. **Concurrent Processing**:
   - Worker Goroutines consume messages from the queue.
   - Relevant data is extracted, filtered, and analyzed.
4. **Real-Time Insights**:
   - Processed results are cached in Redis and pushed back to the Telegram group or stored in the database.

## Links for Reference
- [Mugetsu Documentation](https://mugetsu.gitbook.io/mugetsu)
- [Collab.Land Documentation](https://docs.collab.land/docs/downstream-integrations/)
