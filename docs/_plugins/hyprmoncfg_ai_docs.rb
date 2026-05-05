# frozen_string_literal: true

module Hyprmoncfg
  class AiDocsGenerator < Jekyll::Generator
    safe true
    priority :lowest

    COLLECTIONS = %w[guide reference].freeze

    def generate(site)
      docs = content_docs(site)
      generate_llms(site)
      generate_llms_full(site, docs)
      generate_collection_markdown_resources(site, docs)
    end

    private

    def content_docs(site)
      docs = []
      docs << site.pages.find { |page| page.url == '/' }

      COLLECTIONS.each do |collection_name|
        collection = site.collections[collection_name]
        next unless collection

        docs.concat(collection.docs.sort_by { |doc| [doc.data['nav_order'] || 999, doc.data['title'].to_s] })
      end

      docs.compact
    end

    def generate_llms(site)
      config = site.config.dig('ai_visible_content', 'llms_txt') || {}
      entity = site.config.dig('ai_visible_content', 'entity') || {}

      lines = []
      lines << "# #{config['title'] || site.config['title']}"
      lines << ''
      description = config['description'].to_s.strip
      lines << "> #{description}" unless description.empty?
      lines << ''
      lines << '## About'
      lines << ''
      lines << "#{entity['name']} is #{entity['description'].to_s.strip}"
      lines << ''

      topics = entity['knows_about']
      if topics&.any?
        lines << '## Key Topics'
        lines << ''
        topics.each { |topic| lines << "- #{topic}" }
        lines << ''
      end

      lines << '## Documentation'
      lines << ''
      documentation_links(site).each do |doc|
        lines << "- [#{doc.data['title']}](#{absolute_url(site, doc.url)}): #{doc.data['description']}"
      end
      lines << "- [Full documentation text](#{absolute_url(site, '/llms-full.txt')})"
      lines << ''

      lines << '## Authoritative Source'
      lines << ''
      (entity['same_as'] || []).each { |url| lines << "- #{url}" }
      lines << ''

      add_generated_page(site, '', 'llms.txt', lines.join("\n").strip.concat("\n"))
    end

    def documentation_links(site)
      COLLECTIONS.flat_map do |collection_name|
        collection = site.collections[collection_name]
        next [] unless collection

        collection.docs.sort_by { |doc| [doc.data['nav_order'] || 999, doc.data['title'].to_s] }
      end
    end

    def generate_llms_full(site, docs)
      lines = [
        '# hyprmoncfg Full Documentation',
        '',
        '> Complete source text for hyprmoncfg documentation pages.',
        ''
      ]

      docs.each do |doc|
        title = doc.data['title'] || site.config['title']
        description = doc.data['description'].to_s.strip

        lines << "## #{title}"
        lines << ''
        lines << "URL: #{absolute_url(site, doc.url)}"
        lines << description unless description.empty?
        lines << ''
        lines << document_text(doc)
        lines << ''
        lines << '---'
        lines << ''
      end

      add_generated_page(site, '', 'llms-full.txt', lines.join("\n").strip.concat("\n"))
    end

    def generate_collection_markdown_resources(site, docs)
      docs.each do |doc|
        next unless collection_doc?(doc)

        slug = page_slug(doc)
        content = page_markdown(doc)
        add_generated_page(site, 'ai/page', "#{slug}.txt", content, permalink: "/ai/page/#{slug}.md")
      end
    end

    def collection_doc?(doc)
      doc.respond_to?(:collection) && COLLECTIONS.include?(doc.collection&.label)
    end

    def page_markdown(doc)
      title = doc.data['title'].to_s.strip
      description = doc.data['description'].to_s.strip
      lines = []
      lines << "# #{title}" unless title.empty?
      lines << ''
      lines << description unless description.empty?
      lines << '' unless description.empty?
      lines << document_text(doc)
      lines.join("\n").gsub(/\n{3,}/, "\n\n").strip.concat("\n")
    end

    def document_text(doc)
      body = source_body(doc)
      body.empty? ? front_matter_summary(doc) : body
    end

    def source_body(doc)
      raw = File.exist?(doc.path) ? File.read(doc.path) : doc.content.to_s
      raw.sub(/\A---\s*\n.*?\n---\s*\n?/m, '')
         .gsub(/\{%\s*comment\s*%\}.*?\{%\s*endcomment\s*%\}/m, '')
         .gsub(/\{\{\s*['"]([^'"]+)['"]\s*\|\s*relative_url\s*\}\}/, '\1')
         .gsub(/\{\{\s*['"]([^'"]+)['"]\s*\|\s*absolute_url\s*\}\}/, '\1')
         .gsub(/\{%-?\s*.*?\s*-?%\}/m, '')
         .gsub(/\{\{\s*.*?\s*\}\}/m, '')
         .gsub(/\n{3,}/, "\n\n")
         .strip
    end

    def front_matter_summary(doc)
      lines = []
      hero = doc.data['hero']
      if hero
        lines << hero['text'].to_s.strip
        lines << ''
        lines << hero['tagline'].to_s.strip
      end

      features = doc.data['features']
      if features&.any?
        lines << ''
        lines << 'Features:'
        features.each do |feature|
          lines << "- #{feature['title']}: #{feature['details']}"
        end
      end

      lines.join("\n").gsub(/\n{3,}/, "\n\n").strip
    end

    def add_generated_page(site, dir, name, content, permalink: nil)
      page = Jekyll::PageWithoutAFile.new(site, site.source, dir, name)
      page.content = content
      page.data['layout'] = nil
      page.data['sitemap'] = false
      page.data['permalink'] = permalink if permalink
      site.pages << page
    end

    def absolute_url(site, path)
      "#{site.config['url']}#{path}"
    end

    def page_slug(doc)
      segment = doc.url.to_s.split('/').reject(&:empty?).last
      slugify(segment || doc.data['title'] || 'page')
    end

    def slugify(value)
      value.to_s.downcase.gsub(/[^a-z0-9]+/, '-').gsub(/(^-|-$)/, '')
    end
  end
end
